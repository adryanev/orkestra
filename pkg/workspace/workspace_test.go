package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix Auth":        "fix-auth",
		"  spaces  here ": "spaces-here",
		"weird@@@chars":   "weird-chars",
		"":                "",
		"---":             "",
		"Already-Slug":    "already-slug",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultWorkspaceBranch(t *testing.T) {
	id := "abcdef12-3456-7890-abcd-ef1234567890"
	if got := defaultWorkspaceBranch("Fix Auth", id); got != "orkestra/fix-auth" {
		t.Errorf("got %q, want orkestra/fix-auth", got)
	}
	if got := defaultWorkspaceBranch("", id); got != "orkestra/abcdef12" {
		t.Errorf("got %q, want orkestra/abcdef12", got)
	}
}

// gitInit runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Isolate from the user's global/system git config (gpgsign, hooks, url
	// rewrites) so commits do not block on a GPG passphrase.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// setupRepoWithOrigin creates a repo at <tmp>/repo whose origin/<branch>
// remote-tracking ref exists, returning the repo path.
func setupRepoWithOrigin(t *testing.T, branch string) string {
	t.Helper()
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	repo := filepath.Join(tmp, "repo")

	git(t, tmp, "init", "--bare", "-b", branch, origin)
	git(t, tmp, "clone", origin, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-m", "init")
	git(t, repo, "push", "origin", branch)
	return repo
}

func TestDetectDefaultBranch(t *testing.T) {
	for _, branch := range []string{"main", "master"} {
		repo := setupRepoWithOrigin(t, branch)
		got, err := detectDefaultBranch(repo)
		if err != nil {
			t.Fatalf("branch %s: %v", branch, err)
		}
		if got != branch {
			t.Errorf("detectDefaultBranch = %q, want %q", got, branch)
		}
	}
}

func TestDetectDefaultBranchNoRemote(t *testing.T) {
	tmp := t.TempDir()
	git(t, tmp, "init", filepath.Join(tmp, "local"))
	if _, err := detectDefaultBranch(filepath.Join(tmp, "local")); err == nil {
		t.Error("expected error for repo without origin, got nil")
	}
}

func TestCreateWorkspace(t *testing.T) {
	repo := setupRepoWithOrigin(t, "main")
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ws, err := m.CreateWorkspace("Fix Auth", repo, "", "", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if ws.Branch != "orkestra/fix-auth" {
		t.Errorf("branch = %q, want orkestra/fix-auth", ws.Branch)
	}
	if _, err := os.Stat(ws.WorktreePath); err != nil {
		t.Errorf("worktree not created at %s: %v", ws.WorktreePath, err)
	}
	// README from the origin commit should be present in the worktree.
	if _, err := os.Stat(filepath.Join(ws.WorktreePath, "README.md")); err != nil {
		t.Errorf("worktree missing repo content: %v", err)
	}
}

func TestCreateWorkspaceWithBaseBranch(t *testing.T) {
	repo := setupRepoWithOrigin(t, "develop")
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ws, err := m.CreateWorkspace("Feature", repo, "", "", "develop")
	if err != nil {
		t.Fatalf("CreateWorkspace with base branch: %v", err)
	}
	if ws.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want %q", ws.BaseBranch, "develop")
	}
	if _, err := os.Stat(ws.WorktreePath); err != nil {
		t.Errorf("worktree not created at %s: %v", ws.WorktreePath, err)
	}

	// Passing "origin/develop" should strip the prefix and not double it.
	ws2, err := m.CreateWorkspace("Feature2", repo, "", "", "origin/develop")
	if err != nil {
		t.Fatalf("CreateWorkspace with origin/ prefix: %v", err)
	}
	if ws2.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want develop after stripping prefix", ws2.BaseBranch)
	}
}

func TestCreateWorkspaceRejectsNonRepo(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateWorkspace("x", t.TempDir(), "b", "", ""); err == nil {
		t.Error("expected error for non-git repo path, got nil")
	}
}

func TestRemoveWorkspace(t *testing.T) {
	repo := setupRepoWithOrigin(t, "main")
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := m.CreateWorkspace("Cleanup", repo, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddSession(Session{WorkspaceID: ws.ID, Agent: "claude", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}

	if err := m.RemoveWorkspace(ws.ID, false); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	if _, err := os.Stat(ws.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree directory should be gone, stat err = %v", err)
	}
	if _, err := m.GetWorkspace(ws.ID); err == nil {
		t.Error("workspace should be deregistered")
	}
	if _, err := m.GetSession(ws.ID); err == nil {
		t.Error("session should be removed with the workspace")
	}
}

func TestRemoveWorkspaceUnknownID(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveWorkspace("does-not-exist", false); err == nil {
		t.Error("expected error removing an unknown workspace")
	}
}

func TestRemoveWorkspaceDirtyRequiresForce(t *testing.T) {
	repo := setupRepoWithOrigin(t, "main")
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, err := m.CreateWorkspace("Dirty", repo, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Introduce an uncommitted change so git refuses a non-forced removal.
	if err := os.WriteFile(filepath.Join(ws.WorktreePath, "dirty.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := m.RemoveWorkspace(ws.ID, false); err == nil {
		t.Error("expected dirty worktree removal to be refused without force")
	}
	// The workspace must still be registered after the refused removal.
	if _, err := m.GetWorkspace(ws.ID); err != nil {
		t.Errorf("workspace should survive a refused removal: %v", err)
	}

	if err := m.RemoveWorkspace(ws.ID, true); err != nil {
		t.Fatalf("forced removal should succeed: %v", err)
	}
	if _, err := m.GetWorkspace(ws.ID); err == nil {
		t.Error("workspace should be gone after forced removal")
	}
}

// TestSessionProcessLifecycle verifies that the PID/PGID written at spawn
// survive a reload, that a session-id update preserves them, and that clearing
// zeroes them while keeping the id — the cross-process tracking contract for
// run/stop.
func TestSessionProcessLifecycle(t *testing.T) {
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.SetSessionProcess("ws1", "claude", "", 4242, 4242, 99); err != nil {
		t.Fatal(err)
	}

	// Reload from disk into a fresh manager to prove cross-process durability.
	reloaded, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := reloaded.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PID != 4242 || s.PGID != 4242 || s.StartedAt != 99 {
		t.Errorf("process fields not persisted: %+v", s)
	}

	// An exit-time AddSession records the session id and zeroes process fields.
	if err := reloaded.AddSession(Session{WorkspaceID: "ws1", Agent: "claude", SessionID: "sid-7"}); err != nil {
		t.Fatal(err)
	}
	s, _ = reloaded.GetSession("ws1")
	if s.SessionID != "sid-7" {
		t.Errorf("session id = %q, want sid-7", s.SessionID)
	}
	if s.PID != 0 || s.PGID != 0 {
		t.Errorf("process fields should be cleared after exit, got %+v", s)
	}

	// ClearSessionProcess on a record keeps the id but zeroes process fields.
	if err := reloaded.SetSessionProcess("ws1", "claude", "", 100, 100, 5); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ClearSessionProcess("ws1"); err != nil {
		t.Fatal(err)
	}
	s, _ = reloaded.GetSession("ws1")
	if s.PID != 0 || s.SessionID != "sid-7" {
		t.Errorf("clear should zero pid and keep id, got %+v", s)
	}

	// Clearing a missing workspace is not an error.
	if err := reloaded.ClearSessionProcess("nope"); err != nil {
		t.Errorf("clearing missing session should be a no-op, got %v", err)
	}
}

// TestSetPTYSession verifies that SetPTYSession persists PTY daemon state,
// preserves existing SessionID/ThreadID, and survives reload.
func TestSetPTYSession(t *testing.T) {
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Set up a session with SessionID and Agent.
	if err := m.AddSession(Session{
		WorkspaceID: "ws1",
		Agent:       "claude",
		SessionID:   "sid-1",
	}); err != nil {
		t.Fatal(err)
	}

	// Add PTY state.
	pty := PTYSession{
		SocketPath:  "/tmp/pty.sock",
		DaemonPID:   5000,
		DaemonPGID:  5000,
		DaemonStart: 12345,
		Rows:        24,
		Cols:        80,
	}
	if err := m.SetPTYSession("ws1", pty); err != nil {
		t.Fatal(err)
	}

	// Verify in-memory state.
	s, err := m.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY == nil {
		t.Fatal("PTY should be set")
	}
	if s.PTY.SocketPath != "/tmp/pty.sock" || s.PTY.DaemonPID != 5000 {
		t.Errorf("PTY fields incorrect: %+v", s.PTY)
	}
	if s.SessionID != "sid-1" || s.Agent != "claude" {
		t.Errorf("existing fields clobbered: SessionID=%q, Agent=%q", s.SessionID, s.Agent)
	}

	// Reload from disk and verify persistence.
	reloaded, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err = reloaded.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY == nil {
		t.Fatal("PTY should survive reload")
	}
	if s.PTY.Rows != 24 || s.PTY.Cols != 80 {
		t.Errorf("terminal size not persisted: rows=%d, cols=%d", s.PTY.Rows, s.PTY.Cols)
	}
	if s.SessionID != "sid-1" {
		t.Errorf("SessionID lost after reload: %q", s.SessionID)
	}
}

// TestClearPTYSession verifies that clearing PTY state preserves SessionID/ThreadID.
func TestClearPTYSession(t *testing.T) {
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Set up session with both PTY and SessionID.
	if err := m.AddSession(Session{
		WorkspaceID: "ws1",
		Agent:       "claude",
		SessionID:   "sid-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPTYSession("ws1", PTYSession{
		SocketPath:  "/tmp/pty.sock",
		DaemonPID:   5000,
		DaemonPGID:  5000,
		DaemonStart: 12345,
	}); err != nil {
		t.Fatal(err)
	}

	// Clear PTY.
	if err := m.ClearPTYSession("ws1"); err != nil {
		t.Fatal(err)
	}

	// Verify PTY is nil but SessionID/Agent remain.
	s, err := m.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY != nil {
		t.Error("PTY should be nil after clear")
	}
	if s.SessionID != "sid-1" || s.Agent != "claude" {
		t.Errorf("SessionID/Agent should survive clear: SessionID=%q, Agent=%q", s.SessionID, s.Agent)
	}

	// Clearing a missing workspace should not error.
	if err := m.ClearPTYSession("nope"); err != nil {
		t.Errorf("clearing missing PTY session should be a no-op, got %v", err)
	}
}

// TestPTYSessionConcurrent verifies that concurrent SetPTYSession calls serialize
// correctly under the mutate lock and the last write wins.
func TestPTYSessionConcurrent(t *testing.T) {
	cfg := t.TempDir()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Launch 10 goroutines writing different PIDs.
	done := make(chan int, 10)
	for i := 0; i < 10; i++ {
		pid := 5000 + i
		go func(p int) {
			err := m.SetPTYSession("ws1", PTYSession{
				SocketPath:  "/tmp/pty.sock",
				DaemonPID:   p,
				DaemonPGID:  p,
				DaemonStart: 12345,
			})
			if err != nil {
				t.Errorf("SetPTYSession failed: %v", err)
			}
			done <- p
		}(pid)
	}

	// Wait for all writes.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify the session has one of the written PIDs (last write won).
	s, err := m.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY == nil {
		t.Fatal("PTY should be set")
	}
	if s.PTY.DaemonPID < 5000 || s.PTY.DaemonPID >= 5010 {
		t.Errorf("DaemonPID out of range: %d", s.PTY.DaemonPID)
	}

	// Reload and verify atomicity (the state on disk should be consistent).
	reloaded, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err = reloaded.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY == nil || s.PTY.DaemonPID < 5000 {
		t.Errorf("reloaded state inconsistent: %+v", s.PTY)
	}
}

// TestPTYSessionBackwardCompat verifies that loading a sessions.json without the
// pty field does not error and treats PTY as nil.
func TestPTYSessionBackwardCompat(t *testing.T) {
	cfg := t.TempDir()
	sessionsPath := filepath.Join(cfg, SessionsFile)

	// Write a session without the pty field (old format).
	oldJSON := `{
  "ws1": {
    "workspace_id": "ws1",
    "agent": "claude",
    "session_id": "sid-1"
  }
}`
	if err := os.WriteFile(sessionsPath, []byte(oldJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Load and verify PTY is nil.
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.GetSession("ws1")
	if err != nil {
		t.Fatal(err)
	}
	if s.PTY != nil {
		t.Error("PTY should be nil when loading old sessions.json without pty field")
	}
	if s.SessionID != "sid-1" {
		t.Errorf("SessionID = %q, want sid-1", s.SessionID)
	}

	// Add PTY state and verify it persists.
	if err := m.SetPTYSession("ws1", PTYSession{
		SocketPath:  "/tmp/pty.sock",
		DaemonPID:   6000,
		DaemonPGID:  6000,
		DaemonStart: 99999,
	}); err != nil {
		t.Fatal(err)
	}
	s, _ = m.GetSession("ws1")
	if s.PTY == nil || s.PTY.DaemonPID != 6000 {
		t.Error("PTY should be set after upgrade")
	}
}
