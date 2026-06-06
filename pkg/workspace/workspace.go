package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	DefaultConfigDir = ".orkestra"
	WorkspacesFile   = "workspaces.json"
	SessionsFile     = "sessions.json"
)

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RepoPath    string `json:"repo_path"`
	WorktreePath string `json:"worktree_path"`
	Branch      string `json:"branch"`
	Status      string `json:"status"` // e.g., "active", "inactive", "error"
	GhProfile   string `json:"gh_profile,omitempty"`
}

type Session struct {
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"` // e.g., "claude", "codex"
	SessionID   string `json:"session_id,omitempty"` // agent-specific session ID
	ThreadID    string `json:"thread_id,omitempty"` // agent-specific thread ID (for codex)
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
		if err := os.MkdirAll(configDir, 0755);
			err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", configDir, err)
		}
	}
		
	manager := &Manager{
		configDir: configDir,
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

	// Load workspaces
	workspacesPath := filepath.Join(m.configDir, WorkspacesFile)
	data, err := os.ReadFile(workspacesPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read workspaces file %s: %w", workspacesPath, err)
		}
		// If file doesn't exist, start with empty map
	} else {
		var workspaces []Workspace
		if err := json.Unmarshal(data, &workspaces);
			err != nil {
			return fmt.Errorf("failed to unmarshal workspaces: %w", err)
		}
		for _, ws := range workspaces {
			m.workspaces[ws.ID] = &ws
		}
	}

	// Load sessions
	sessionsPath := filepath.Join(m.configDir, SessionsFile)
	data, err = os.ReadFile(sessionsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read sessions file %s: %w", sessionsPath, err)
		}
		// If file doesn't exist, start with empty map
	} else {
		var sessions []Session
		if err := json.Unmarshal(data, &sessions);
			err != nil {
			return fmt.Errorf("failed to unmarshal sessions: %w", err)
		}
		for _, s := range sessions {
			m.sessions[s.WorkspaceID] = &s
		}
	}

	return nil
}

// Save persists the current state of workspaces and sessions to disk.
func (m *Manager) Save() error { 
	m.Lock()
	defer m.Unlock()

	// Save workspaces
	workspacesPath := filepath.Join(m.configDir, WorkspacesFile)
	workspacesData, err := json.MarshalIndent(m.workspaces, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workspaces: %w", err)
	}
	if err := os.WriteFile(workspacesPath, workspacesData, 0644);
		err != nil {
		return fmt.Errorf("failed to write workspaces file %s: %w", workspacesPath, err)
	}

	// Save sessions
	sessionsPath := filepath.Join(m.configDir, SessionsFile)
	sessionsData, err := json.MarshalIndent(m.sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}
	if err := os.WriteFile(sessionsPath, sessionsData, 0644);
		err != nil {
		return fmt.Errorf("failed to write sessions file %s: %w", sessionsPath, err)
	}

	return nil
}

func (m *Manager) CreateWorkspace(name, repoPath, branch, ghProfile string) (*Workspace, error) {
	m.Lock()
	defer m.Unlock()

	id := uuid.New().String()

	// Detect default branch
	defaultBranch := "origin/main"
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		defaultBranch = strings.TrimSpace(string(out))
	}

	// Create worktree
	worktreePath := m.configDir + "/worktrees/" + id
	os.MkdirAll(m.configDir+"/worktrees", 0755)

	if err := exec.Command("git", "worktree", "add", "-b", branch, worktreePath, defaultBranch).Run(); err != nil {
		return nil, fmt.Errorf("failed to create worktree: %w", err)
	}

	ws := &Workspace{
		ID:          id,
		Name:        name,
		RepoPath:    repoPath,
		WorktreePath: worktreePath,
		Branch:      branch,
		Status:      "active",
		GhProfile:   ghProfile,
	}

	m.workspaces[id] = ws

	if err := m.Save(); err != nil {
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
	m.Lock()
	defer m.Unlock()

	ws, ok := m.workspaces[id]
	if !ok {
		return fmt.Errorf("workspace with id %s not found", id)
	}
	ws.Status = status

	if err := m.Save(); err != nil {
		return fmt.Errorf("failed to save workspace status: %w", err)
	}
	return nil
}

func (m *Manager) AddSession(session Session) error {
	m.Lock()
	defer m.Unlock()

	m.sessions[session.WorkspaceID] = &session

	if err := m.Save(); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
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

func (m *Manager) RemoveSession(workspaceID string) error {
	m.Lock()
	defer m.Unlock()

	_, ok := m.sessions[workspaceID]
	if !ok {
		return fmt.Errorf("session for workspace %s not found", workspaceID)
	}
	delete(m.sessions, workspaceID)

	if err := m.Save(); err != nil {
		return fmt.Errorf("failed to save sessions after removal: %w", err)
	}
	return nil
}
