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

// TestDaemon_Minimal tests basic daemon startup and shutdown
func TestDaemon_Minimal(t *testing.T) {
	// Create temp workspace manager
	tmpDir := t.TempDir()
	mgr, err := workspace.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Create minimal git repo
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatal(err)
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

	socketPath := filepath.Join(tmpDir, "daemon.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use simple command that exits immediately
	cmd := exec.Command("sh", "-c", "echo started; exit 0")

	cfg := DaemonConfig{
		WorkspaceID: ws.ID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	// Run daemon
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunDaemon(ctx, cfg)
	}()

	// Wait a bit for daemon to start
	time.Sleep(200 * time.Millisecond)

	// Try to connect
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Logf("failed to connect (daemon may have exited): %v", err)
		// Wait for daemon to finish
		select {
		case daemonErr := <-errCh:
			t.Logf("daemon exited with: %v", daemonErr)
		case <-time.After(time.Second):
			t.Fatal("daemon did not exit")
		}
		return
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	// Should get MsgReady
	if scanner.Scan() {
		msg, _ := Decode(scanner.Bytes())
		t.Logf("Got message: %s", msg.Type)
	}

	t.Log("Test completed successfully")
}

// TestDaemon_CatEcho tests with 'cat' command
func TestDaemon_CatEcho(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := workspace.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Create minimal git repo
	repoPath := filepath.Join(tmpDir, "repo")
	os.MkdirAll(repoPath, 0755)

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
		cmd.Run()
	}

	ws, err := mgr.CreateWorkspace("test", repoPath, "", "", "main")
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	socketPath := filepath.Join(tmpDir, "daemon.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use sh -c with a sleep to keep process alive
	cmd := exec.Command("sh", "-c", "while read line; do echo \"$line\"; done")

	cfg := DaemonConfig{
		WorkspaceID: ws.ID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	// Run daemon
	go func() {
		err := RunDaemon(ctx, cfg)
		if err != nil {
			t.Logf("daemon error: %v", err)
		}
	}()

	// Wait for socket
	var conn net.Conn
	for i := 0; i < 10; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("failed to connect to daemon")
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	// Skip MsgReady and MsgBuffer
	if !scanner.Scan() {
		t.Fatal("expected MsgReady")
	}
	msg1, _ := Decode(scanner.Bytes())
	t.Logf("Got: %s", msg1.Type)

	if !scanner.Scan() {
		t.Fatal("expected MsgBuffer")
	}
	msg2, _ := Decode(scanner.Bytes())
	t.Logf("Got: %s", msg2.Type)
	if msg2.Type == MsgExit {
		if msg2.Code != nil {
			t.Fatalf("agent exited with code %d", *msg2.Code)
		}
		t.Fatal("agent exited unexpectedly")
	}

	// Send input
	inputMsg := Msg{Type: MsgInput, Data: EncodeData([]byte("test\n"))}
	encoded, _ := Encode(inputMsg)
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("failed to write input: %v", err)
	}
	t.Log("Sent input")

	// Read output with timeout
	outputCh := make(chan Msg, 1)
	errCh := make(chan error, 1)
	go func() {
		if scanner.Scan() {
			msg, err := Decode(scanner.Bytes())
			if err != nil {
				errCh <- err
				return
			}
			outputCh <- msg
		} else {
			if err := scanner.Err(); err != nil {
				errCh <- err
			}
		}
	}()

	select {
	case msg := <-outputCh:
		t.Logf("Received message type: %s", msg.Type)
		if msg.Type != MsgOutput {
			t.Errorf("expected MsgOutput, got %s", msg.Type)
		}
		data, _ := DecodeData(msg.Data)
		// PTY adds \r\n due to terminal line discipline
		expected := "test\r\n"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, data)
		} else {
			t.Log("Successfully echoed input!")
		}
	case err := <-errCh:
		t.Fatalf("scanner error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for output")
	}
}
