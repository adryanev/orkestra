package runner

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/workspace"
)

// registerWorkspace writes a workspaces.json containing one workspace and
// returns a manager over cfg plus the workspace id.
func registerWorkspace(t *testing.T, cfg, worktree string) (*workspace.Manager, string) {
	t.Helper()
	id := "ws-int"
	content := `{"` + id + `":{"id":"` + id + `","name":"int","repo_path":"` + cfg +
		`","worktree_path":"` + worktree + `","branch":"main","status":"active"}}`
	if err := os.WriteFile(filepath.Join(cfg, "workspaces.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := workspace.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m, id
}

// TestRunCapturesSessionAndClearsProcess drives the full executeAgent path with
// a fake agent that emits stream-json. It proves the session id is captured
// deterministically (drain-then-wait ordering, KTD6) and that the process
// record is persisted during the run and cleared on exit (U7). Run under -race
// to catch any data race on SessionInfo.
func TestRunCapturesSessionAndClearsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake agent uses a POSIX shell script")
	}
	cfg := t.TempDir()
	worktree := filepath.Join(cfg, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	m, id := registerWorkspace(t, cfg, worktree)

	// A fake claude that emits a system line (session id), an assistant line,
	// and a result line with usage, then exits.
	fake := filepath.Join(cfg, "fake-claude")
	script := "#!/bin/sh\n" +
		`echo '{"type":"system","session_id":"sid-INT"}'` + "\n" +
		`echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}'` + "\n" +
		`echo '{"type":"result","model":"opus","usage":{"input_tokens":3,"cache_read_input_tokens":1,"output_tokens":2}}'` + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(m)
	r.binOverride = fake

	si, err := r.Run(id, Claude, "do it", false, false, true, "", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if si.SessionID != "sid-INT" {
		t.Errorf("session id = %q, want sid-INT", si.SessionID)
	}
	if si.Usage.InputTokens != 4 { // 3 + cache_read 1
		t.Errorf("input tokens = %d, want 4", si.Usage.InputTokens)
	}

	// The session id is persisted and the process record is cleared on exit.
	saved, err := m.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if saved.SessionID != "sid-INT" {
		t.Errorf("persisted session id = %q, want sid-INT", saved.SessionID)
	}
	if saved.PID != 0 || saved.PGID != 0 {
		t.Errorf("process record should be cleared after exit, got pid=%d pgid=%d", saved.PID, saved.PGID)
	}
}

func TestRunCanSuppressRenderedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake agent uses a POSIX shell script")
	}
	cfg := t.TempDir()
	worktree := filepath.Join(cfg, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	m, id := registerWorkspace(t, cfg, worktree)

	fake := filepath.Join(cfg, "fake-claude")
	script := "#!/bin/sh\n" +
		`echo '{"type":"system","session_id":"sid-JSON"}'` + "\n" +
		`echo '{"type":"assistant","message":{"content":[{"type":"text","text":"human text"}]}}'` + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(m)
	runner.binOverride = fake

	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeEnd
	si, runErr := runner.Run(id, Claude, "do it", false, false, false, "", "")
	_ = writeEnd.Close()
	os.Stdout = oldStdout
	out, readErr := io.ReadAll(readEnd)
	_ = readEnd.Close()

	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if string(out) != "" {
		t.Fatalf("renderOutput=false should suppress live stdout, got %q", out)
	}
	if si.SessionID != "sid-JSON" {
		t.Errorf("session id = %q, want sid-JSON", si.SessionID)
	}
}

func TestRunFailsWhenProcessStateCannotBePersisted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake agent uses a POSIX shell script")
	}
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "cfg")
	worktree := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	m, id := registerWorkspace(t, cfg, worktree)

	pidFile := filepath.Join(tmp, "agent.pid")
	fake := filepath.Join(tmp, "fake-claude")
	script := "#!/bin/sh\n" +
		"echo $$ > " + pidFile + "\n" +
		"sleep 30\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Turn sessions.json into a directory after the manager has loaded. MCP
	// config creation still succeeds, but SetSessionProcess fails after
	// cmd.Start when it tries to reload the sessions state file.
	sessionsPath := filepath.Join(cfg, "sessions.json")
	if err := os.RemoveAll(sessionsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sessionsPath, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(m)
	runner.binOverride = fake
	_, err := runner.Run(id, Claude, "do it", false, false, false, "", "")
	if err == nil || !strings.Contains(err.Error(), "failed to persist agent process") {
		t.Fatalf("expected process persistence error, got %v", err)
	}

	data, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		return // The process was killed before the script wrote its pid.
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for process.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if process.Alive(pid) {
		t.Fatalf("agent process %d should have been terminated after persistence failure", pid)
	}
}
