package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adryanev/orkestra/pkg/state"
	"github.com/google/uuid"
)

// Git operation deadlines. Detection probes are fast; a worktree add checks out
// files and is given more headroom. No git call may block indefinitely (R11).
const (
	gitDetectTimeout   = 15 * time.Second
	gitWorktreeTimeout = 2 * time.Minute
)

const (
	DefaultConfigDir = ".orkestra"
	WorkspacesFile   = "workspaces.json"
	SessionsFile     = "sessions.json"
)

type Workspace struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RepoPath     string `json:"repo_path"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	Status       string `json:"status"` // e.g., "active", "inactive", "error"
	GhProfile    string `json:"gh_profile,omitempty"`
}

type Session struct {
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`                // e.g., "claude", "codex"
	SessionID   string `json:"session_id,omitempty"` // agent-specific session ID
	ThreadID    string `json:"thread_id,omitempty"`  // agent-specific thread ID (for codex)
	// Process tracking for cross-process `stop`. PID/PGID identify the running
	// agent; StartedAt (unix nanoseconds) records when `run` spawned it.
	PID       int   `json:"pid,omitempty"`
	PGID      int   `json:"pgid,omitempty"`
	StartedAt int64 `json:"started_at,omitempty"`
}

type Manager struct {
	configDir string
	sync.Mutex
	workspaces map[string]*Workspace
	sessions   map[string]*Session
}

func NewManager(configDir string) (*Manager, error) {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		configDir = filepath.Join(home, DefaultConfigDir)
	}

	_, err := os.Stat(configDir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}

	manager := &Manager{
		configDir:  configDir,
		workspaces: make(map[string]*Workspace),
		sessions:   make(map[string]*Session),
	}

	if err := manager.load(); err != nil {
		return nil, fmt.Errorf("failed to load workspace and session data: %w", err)
	}

	return manager, nil
}

func (m *Manager) load() error {
	m.Lock()
	defer m.Unlock()
	return m.readState()
}

// lockPath is the single advisory lock file guarding all state writes.
func (m *Manager) lockPath() string {
	return filepath.Join(m.configDir, ".state.lock")
}

// readState replaces the in-memory maps with the current on-disk state. The
// caller must hold m's mutex. Atomic writes (rename) mean a read always sees a
// complete file, so no file lock is required to read.
func (m *Manager) readState() error {
	m.workspaces = make(map[string]*Workspace)
	m.sessions = make(map[string]*Session)

	workspacesPath := filepath.Join(m.configDir, WorkspacesFile)
	data, err := state.ReadFile(workspacesPath)
	if err != nil {
		return fmt.Errorf("failed to read workspaces file %s: %w", workspacesPath, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m.workspaces); err != nil {
			return fmt.Errorf("failed to unmarshal workspaces: %w", err)
		}
	}

	sessionsPath := filepath.Join(m.configDir, SessionsFile)
	data, err = state.ReadFile(sessionsPath)
	if err != nil {
		return fmt.Errorf("failed to read sessions file %s: %w", sessionsPath, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m.sessions); err != nil {
			return fmt.Errorf("failed to unmarshal sessions: %w", err)
		}
	}

	return nil
}

// mutate runs apply against the in-memory maps under both the in-process mutex
// and a cross-process advisory file lock, after reloading the latest on-disk
// state, then writes the result atomically. This makes the full read-modify-write
// cycle safe against concurrent orkestra processes: a parallel writer's keys are
// reloaded before apply runs, so they are never clobbered.
func (m *Manager) mutate(apply func() error) error {
	m.Lock()
	defer m.Unlock()

	lock, err := state.Acquire(m.lockPath())
	if err != nil {
		return fmt.Errorf("failed to acquire state lock: %w", err)
	}
	defer lock.Release()

	if err := m.readState(); err != nil {
		return err
	}
	if err := apply(); err != nil {
		return err
	}
	return m.saveLocked()
}

// Save persists the current state of workspaces and sessions to disk.
func (m *Manager) Save() error {
	m.Lock()
	defer m.Unlock()
	return m.saveLocked()
}

// saveLocked persists state assuming the caller already holds m's lock. Mutating
// methods that hold the lock must call this instead of Save to avoid a
// self-deadlock on the non-reentrant mutex.
func (m *Manager) saveLocked() error {
	// Save workspaces
	workspacesPath := filepath.Join(m.configDir, WorkspacesFile)
	workspacesData, err := json.MarshalIndent(m.workspaces, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workspaces: %w", err)
	}
	if err := state.WriteAtomic(workspacesPath, workspacesData, 0644); err != nil {
		return fmt.Errorf("failed to write workspaces file %s: %w", workspacesPath, err)
	}

	// Save sessions
	sessionsPath := filepath.Join(m.configDir, SessionsFile)
	sessionsData, err := json.MarshalIndent(m.sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}
	if err := state.WriteAtomic(sessionsPath, sessionsData, 0644); err != nil {
		return fmt.Errorf("failed to write sessions file %s: %w", sessionsPath, err)
	}

	return nil
}

func (m *Manager) CreateWorkspace(name, repoPath, branch, ghProfile string) (*Workspace, error) {
	if repoPath == "" {
		return nil, fmt.Errorf("repo path is required")
	}
	if !isGitRepo(repoPath) {
		return nil, fmt.Errorf("%q is not a git repository", repoPath)
	}

	id := uuid.New().String()

	// Detect the default branch of the target repository (origin/HEAD ->
	// origin/main -> origin/master). All git commands run inside repoPath.
	defaultBranch, err := detectDefaultBranch(repoPath)
	if err != nil {
		return nil, err
	}

	// Generate a valid branch name when none was supplied; an empty value
	// would make `git worktree add -b ""` fail.
	if branch == "" {
		branch = defaultWorkspaceBranch(name, id)
	}

	// Create worktree from origin/<defaultBranch>.
	worktreePath := filepath.Join(m.configDir, "worktrees", id)
	if err := os.MkdirAll(filepath.Join(m.configDir, "worktrees"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktrees directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), gitWorktreeTimeout)
	defer cancel()
	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, worktreePath, "origin/"+defaultBranch)
	addCmd.Dir = repoPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w: %s", err, strings.TrimSpace(string(out)))
	}

	ws := &Workspace{
		ID:           id,
		Name:         name,
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Branch:       branch,
		Status:       "active",
		GhProfile:    ghProfile,
	}

	if err := m.mutate(func() error {
		m.workspaces[id] = ws
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to save workspace: %w", err)
	}

	return ws, nil
}

func (m *Manager) ListWorkspaces() ([]Workspace, error) {
	m.Lock()
	defer m.Unlock()

	var workspaces []Workspace
	for _, ws := range m.workspaces {
		workspaces = append(workspaces, *ws)
	}
	return workspaces, nil
}

func (m *Manager) GetWorkspace(id string) (*Workspace, error) {
	m.Lock()
	defer m.Unlock()

	ws, ok := m.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("workspace with id %s not found", id)
	}
	return ws, nil
}

func (m *Manager) UpdateWorkspaceStatus(id, status string) error {
	return m.mutate(func() error {
		ws, ok := m.workspaces[id]
		if !ok {
			return fmt.Errorf("workspace with id %s not found", id)
		}
		ws.Status = status
		return nil
	})
}

func (m *Manager) AddSession(session Session) error {
	return m.mutate(func() error {
		s := session
		m.sessions[s.WorkspaceID] = &s
		return nil
	})
}

// SetSessionProcess records the running agent's PID/PGID and start time for a
// workspace so a separate `stop` process can signal it. It preserves any
// existing session/thread id and agent on the record (a resume already has
// one), creating a fresh record when none exists.
func (m *Manager) SetSessionProcess(workspaceID, agent string, pid, pgid int, startedAt int64) error {
	return m.mutate(func() error {
		s, ok := m.sessions[workspaceID]
		if !ok {
			s = &Session{WorkspaceID: workspaceID}
			m.sessions[workspaceID] = s
		}
		if agent != "" {
			s.Agent = agent
		}
		s.PID = pid
		s.PGID = pgid
		s.StartedAt = startedAt
		return nil
	})
}

// ClearSessionProcess zeroes the process-tracking fields for a workspace,
// leaving the session/thread id intact. A missing record is not an error so
// callers can clear unconditionally on exit.
func (m *Manager) ClearSessionProcess(workspaceID string) error {
	return m.mutate(func() error {
		if s, ok := m.sessions[workspaceID]; ok {
			s.PID = 0
			s.PGID = 0
			s.StartedAt = 0
		}
		return nil
	})
}

func (m *Manager) GetSession(workspaceID string) (*Session, error) {
	m.Lock()
	defer m.Unlock()

	s, ok := m.sessions[workspaceID]
	if !ok {
		return nil, fmt.Errorf("session for workspace %s not found", workspaceID)
	}
	return s, nil
}

// isGitRepo reports whether path is inside a git working tree.
func isGitRepo(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitDetectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// detectDefaultBranch resolves the default branch name of the repo at repoPath,
// trying origin/HEAD, then origin/main, then origin/master. It returns the bare
// branch name (e.g. "main"), not a ref. Each git call runs under a deadline.
func detectDefaultBranch(repoPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitDetectTimeout)
	defer cancel()

	// Tier 1: the origin/HEAD symref, the authoritative default.
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = repoPath
	if out, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(out))
		if name := strings.TrimPrefix(ref, "refs/remotes/origin/"); name != ref && name != "" {
			return name, nil
		}
	}

	// Tier 2: probe common defaults.
	for _, name := range []string{"main", "master"} {
		probe := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", "origin/"+name)
		probe.Dir = repoPath
		if probe.Run() == nil {
			return name, nil
		}
	}

	return "", fmt.Errorf("could not detect default branch for %q (no origin/HEAD, origin/main, or origin/master)", repoPath)
}

// defaultWorkspaceBranch builds a valid branch name from the workspace name,
// falling back to a short id-based name when no name is available.
func defaultWorkspaceBranch(name, id string) string {
	slug := slugify(name)
	if slug == "" {
		return "orkestra/" + id[:8]
	}
	return "orkestra/" + slug
}

// slugify reduces s to a git-branch-safe slug: lowercase alphanumerics and
// single hyphens, no leading/trailing hyphen.
func slugify(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// RemoveWorkspace removes the git worktree for a workspace and then
// deregisters the workspace and its session. git worktree remove refuses a
// dirty worktree unless force is set, which is the safety we want; pass force
// to discard uncommitted changes. The caller is responsible for terminating
// any running agent before calling this.
func (m *Manager) RemoveWorkspace(id string, force bool) error {
	m.Lock()
	ws, ok := m.workspaces[id]
	if !ok {
		m.Unlock()
		return fmt.Errorf("workspace with id %s not found", id)
	}
	repoPath := ws.RepoPath
	worktreePath := ws.WorktreePath
	m.Unlock()

	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = []string{"worktree", "remove", "--force", worktreePath}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitDetectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove worktree for %s: %w: %s", id, err, strings.TrimSpace(string(out)))
	}

	return m.mutate(func() error {
		delete(m.workspaces, id)
		delete(m.sessions, id)
		return nil
	})
}

func (m *Manager) RemoveSession(workspaceID string) error {
	return m.mutate(func() error {
		if _, ok := m.sessions[workspaceID]; !ok {
			return fmt.Errorf("session for workspace %s not found", workspaceID)
		}
		delete(m.sessions, workspaceID)
		return nil
	})
}
