//go:build !windows

package pty

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/workspace"
)

// TestDaemon_WithSleep tests daemon with a long-running process
func TestDaemon_WithSleep(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := workspace.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Create minimal git repo
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "commit", "--allow-empty", "-m", "init"},
		{"git", "branch", "-M", "main"},
		{"git", "remote", "add", "origin", "https://example.com/repo.git"},
		{"git", "update-ref", "refs/remotes/origin/main", "HEAD"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoPath
		if err := cmd.Run(); err != nil {
			t.Fatalf("git command failed %v: %v", args, err)
		}
	}

	ws, err := mgr.CreateWorkspace("test", repoPath, "", "", "main")
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	socketPath := testSocketPath(t, "daemon.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use sleep to keep process alive
	cmd := exec.Command("sleep", "10")

	cfg := DaemonConfig{
		WorkspaceID: ws.ID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	// Run daemon
	daemonDone := make(chan struct{})
	go func() {
		if err := RunDaemon(ctx, cfg); err != nil {
			t.Logf("daemon error: %v", err)
		}
		close(daemonDone)
	}()
	defer func() {
		cancel()
		select {
		case <-daemonDone:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon did not exit")
		}
	}()

	// Wait for socket
	var conn net.Conn
	for i := 0; i < 20; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("failed to connect to daemon")
	}
	defer closeTestConn(t, conn)

	// Send MsgAttach before expecting MsgReady
	attachMsg := Msg{Type: MsgAttach, Rows: 24, Cols: 80}
	encoded, err := Encode(attachMsg)
	if err != nil {
		t.Fatalf("encode attach: %v", err)
	}
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("write attach: %v", err)
	}

	scanner := bufio.NewScanner(conn)

	// Should get MsgReady
	if !scanner.Scan() {
		t.Fatal("expected MsgReady")
	}
	msg1, _ := Decode(scanner.Bytes())
	t.Logf("Got: %s - %s", msg1.Type, msg1.Message)
	if msg1.Type != MsgReady {
		t.Errorf("expected MsgReady, got %s", msg1.Type)
	}

	// Should get MsgBuffer (empty)
	if !scanner.Scan() {
		t.Fatal("expected MsgBuffer")
	}
	msg2, _ := Decode(scanner.Bytes())
	t.Logf("Got: %s", msg2.Type)
	if msg2.Type != MsgBuffer {
		t.Errorf("expected MsgBuffer, got %s", msg2.Type)
	}

	// Process should still be running
	session, err := mgr.GetSession(ws.ID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if session.PID == 0 {
		t.Error("expected PID to be set")
	}

	t.Log("Test passed!")
}
