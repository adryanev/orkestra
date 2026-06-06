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

	ws, err := m.CreateWorkspace("Fix Auth", repo, "", "")
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

func TestCreateWorkspaceRejectsNonRepo(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateWorkspace("x", t.TempDir(), "b", ""); err == nil {
		t.Error("expected error for non-git repo path, got nil")
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

	if err := m.SetSessionProcess("ws1", "claude", 4242, 4242, 99); err != nil {
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
	if err := reloaded.SetSessionProcess("ws1", "claude", 100, 100, 5); err != nil {
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
