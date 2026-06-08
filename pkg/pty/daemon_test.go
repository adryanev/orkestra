//go:build !windows

package pty

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/workspace"
)

// Helper to create a temporary workspace manager for testing
func setupTestManager(t *testing.T) (*workspace.Manager, string) {
	t.Helper()
	tmpDir := t.TempDir()
	mgr, err := workspace.NewManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create test manager: %v", err)
	}
	return mgr, tmpDir
}

// Helper to create a test workspace
func createTestWorkspace(t *testing.T, mgr *workspace.Manager, tmpDir string) string {
	t.Helper()

	// Create a minimal git repo for the workspace
	repoPath := filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	// Initialize git repo with origin remote
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test User"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
		{"git", "branch", "-M", "main"},
		{"git", "remote", "add", "origin", "https://github.com/test/test.git"},
		{"git", "update-ref", "refs/remotes/origin/main", "HEAD"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoPath
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %v: %v", args, err)
		}
	}

	// Create workspace
	ws, err := mgr.CreateWorkspace("test-ws", repoPath, "", "", "main")
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	return ws.ID
}

// Helper to connect to the daemon socket
func connectToDaemon(socketPath string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			return conn, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("failed to connect to daemon within timeout")
}

// sendAttach sends MsgAttach with a default terminal size on conn.
// Must be called before reading MsgReady from a freshly connected daemon.
func sendAttach(t *testing.T, conn net.Conn) {
	t.Helper()
	msg := Msg{Type: MsgAttach, Rows: 24, Cols: 80}
	encoded, err := Encode(msg)
	if err != nil {
		t.Fatalf("encode attach: %v", err)
	}
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("send attach: %v", err)
	}
}

func TestEnsureSocketDirRestrictsPermissions(t *testing.T) {
	parent := t.TempDir()
	socketDir := filepath.Join(parent, "pty")
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		t.Fatalf("failed to create socket dir: %v", err)
	}

	if err := ensureSocketDir(filepath.Join(socketDir, "test.sock")); err != nil {
		t.Fatalf("ensureSocketDir failed: %v", err)
	}

	info, err := os.Stat(socketDir)
	if err != nil {
		t.Fatalf("failed to stat socket dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("socket dir permissions = %04o, want 0700", got)
	}
}

// TestDaemon_HappyPath tests the basic daemon functionality with 'cat' as agent
func TestDaemon_HappyPath(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use 'cat' as a simple echo agent
	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	// Run daemon in background
	var daemonErr error
	daemonDone := make(chan struct{})
	daemonStarted := make(chan error, 1)
	go func() {
		daemonErr = RunDaemon(ctx, cfg)
		if daemonErr != nil {
			select {
			case daemonStarted <- daemonErr:
			default:
			}
		}
		close(daemonDone)
	}()

	// Give daemon time to start
	select {
	case err := <-daemonStarted:
		t.Fatalf("daemon failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Daemon started successfully
	}

	// Connect client and attach
	conn, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to daemon: %v", err)
	}
	defer conn.Close()
	sendAttach(t, conn)

	scanner := bufio.NewScanner(conn)

	// Expect MsgReady
	if !scanner.Scan() {
		t.Fatal("expected MsgReady")
	}
	msg, err := Decode(scanner.Bytes())
	if err != nil {
		t.Fatalf("failed to decode MsgReady: %v", err)
	}
	if msg.Type != MsgReady {
		t.Errorf("expected MsgReady, got %s", msg.Type)
	}

	// Expect MsgBuffer (may be empty)
	if !scanner.Scan() {
		t.Fatal("expected MsgBuffer")
	}
	msg, err = Decode(scanner.Bytes())
	if err != nil {
		t.Fatalf("failed to decode MsgBuffer: %v", err)
	}
	if msg.Type != MsgBuffer {
		t.Errorf("expected MsgBuffer, got %s", msg.Type)
	}

	// Send input
	inputMsg := Msg{
		Type: MsgInput,
		Data: EncodeData([]byte("hello\n")),
	}
	encoded, err := Encode(inputMsg)
	if err != nil {
		t.Fatalf("failed to encode input: %v", err)
	}
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("failed to write input: %v", err)
	}

	// Expect output
	if !scanner.Scan() {
		t.Fatal("expected MsgOutput")
	}
	msg, err = Decode(scanner.Bytes())
	if err != nil {
		t.Fatalf("failed to decode MsgOutput: %v", err)
	}
	if msg.Type != MsgOutput {
		t.Errorf("expected MsgOutput, got %s", msg.Type)
	}
	outputData, err := DecodeData(msg.Data)
	if err != nil {
		t.Fatalf("failed to decode output data: %v", err)
	}
	if !strings.Contains(string(outputData), "hello\r\n") {
		t.Errorf("expected output to contain 'hello\\r\\n', got %q", outputData)
	}

	// Detach
	detachMsg := Msg{Type: MsgDetach}
	encoded, _ = Encode(detachMsg)
	conn.Write(encoded)
	conn.Close()

	// Stop daemon
	cancel()

	select {
	case <-daemonDone:
		// Daemon exited
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit within timeout")
	}

	if daemonErr != nil && !strings.Contains(daemonErr.Error(), "context canceled") {
		t.Errorf("unexpected daemon error: %v", daemonErr)
	}
}

// TestDaemon_RingBufferReplay tests that new clients receive buffered history
func TestDaemon_RingBufferReplay(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use 'cat' as agent
	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	// Run daemon in background
	daemonDone := make(chan struct{})
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// First client: send some data
	conn1, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect client 1: %v", err)
	}
	sendAttach(t, conn1)

	scanner := bufio.NewScanner(conn1)
	// Skip MsgReady and MsgBuffer
	scanner.Scan()
	scanner.Scan()

	// Send input
	inputMsg := Msg{
		Type: MsgInput,
		Data: EncodeData([]byte("buffered data\n")),
	}
	encoded, _ := Encode(inputMsg)
	conn1.Write(encoded)

	// Wait for output
	scanner.Scan()

	// Detach client 1
	detachMsg := Msg{Type: MsgDetach}
	encoded, _ = Encode(detachMsg)
	conn1.Write(encoded)
	conn1.Close()

	time.Sleep(50 * time.Millisecond)

	// Second client: should receive buffered history
	conn2, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect client 2: %v", err)
	}
	defer conn2.Close()
	sendAttach(t, conn2)

	scanner2 := bufio.NewScanner(conn2)

	// Skip MsgReady
	if !scanner2.Scan() {
		t.Fatal("expected MsgReady for client 2")
	}

	// Expect MsgBuffer with history
	if !scanner2.Scan() {
		t.Fatal("expected MsgBuffer for client 2")
	}
	msg, err := Decode(scanner2.Bytes())
	if err != nil {
		t.Fatalf("failed to decode MsgBuffer: %v", err)
	}
	if msg.Type != MsgBuffer {
		t.Errorf("expected MsgBuffer, got %s", msg.Type)
	}

	bufferData, err := DecodeData(msg.Data)
	if err != nil {
		t.Fatalf("failed to decode buffer data: %v", err)
	}
	if !strings.Contains(string(bufferData), "buffered data") {
		t.Errorf("expected buffer to contain 'buffered data', got %q", bufferData)
	}

	// Clean up
	cancel()
	<-daemonDone
}

// TestDaemon_ConcurrentAttachRejection tests that only one client can attach at a time
func TestDaemon_ConcurrentAttachRejection(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// Connect first client
	conn1, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect client 1: %v", err)
	}
	defer conn1.Close()
	sendAttach(t, conn1)

	scanner1 := bufio.NewScanner(conn1)
	// Skip MsgReady and MsgBuffer
	scanner1.Scan()
	scanner1.Scan()

	// Try to connect second client while first is attached
	conn2, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect client 2: %v", err)
	}
	defer conn2.Close()

	scanner2 := bufio.NewScanner(conn2)
	// Should receive MsgError about concurrent attach
	if !scanner2.Scan() {
		t.Fatal("expected MsgError for concurrent attach")
	}
	msg, err := Decode(scanner2.Bytes())
	if err != nil {
		t.Fatalf("failed to decode message: %v", err)
	}
	if msg.Type != MsgError {
		t.Errorf("expected MsgError, got %s", msg.Type)
	}
	if !strings.Contains(msg.Message, "already attached") {
		t.Errorf("expected error about concurrent attach, got %q", msg.Message)
	}

	// Clean up
	cancel()
	<-daemonDone
}

// TestDaemon_AgentExitPropagation tests that agent exit is propagated to client
func TestDaemon_AgentExitPropagation(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Keep the agent alive long enough for the client to attach, then verify
	// the daemon propagates its exit status.
	cmd := exec.Command("sh", "-c", "sleep 0.5; exit 42")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	sendAttach(t, conn)

	scanner := bufio.NewScanner(conn)

	// Skip MsgReady and MsgBuffer
	scanner.Scan()
	scanner.Scan()

	// Wait for MsgExit
	foundExit := false
	for scanner.Scan() {
		msg, err := Decode(scanner.Bytes())
		if err != nil {
			continue
		}
		if msg.Type == MsgExit {
			foundExit = true
			if msg.Code == nil || *msg.Code != 42 {
				t.Errorf("expected exit code 42, got %v", msg.Code)
			}
			break
		}
	}

	if !foundExit {
		t.Error("expected MsgExit, but did not receive it")
	}

	// Daemon should exit after agent exits
	select {
	case <-daemonDone:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit after agent exit")
	}

	// Verify session process was cleared
	session, err := mgr.GetSession(wsID)
	if err == nil {
		if session.PID != 0 || session.PGID != 0 {
			t.Errorf("expected process cleared, got PID=%d PGID=%d", session.PID, session.PGID)
		}
	}
}

// TestDaemon_ResizePropagation tests that resize messages are propagated to PTY
func TestDaemon_ResizePropagation(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	var sessionPtr *PTYSession
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	sendAttach(t, conn)

	scanner := bufio.NewScanner(conn)
	// Skip MsgReady and MsgBuffer
	scanner.Scan()
	scanner.Scan()

	// Send resize message
	resizeMsg := Msg{
		Type: MsgResize,
		Rows: 50,
		Cols: 120,
	}
	encoded, _ := Encode(resizeMsg)
	conn.Write(encoded)

	// Note: We can't easily verify the resize was applied without more
	// intrusive testing, but we can verify the message is accepted without error.
	// The real verification would be in an integration test with a real terminal.

	time.Sleep(50 * time.Millisecond)

	// Clean up
	cancel()
	<-daemonDone

	_ = sessionPtr // avoid unused warning
}

// TestDaemon_ContextCancellation tests that daemon stops cleanly on context cancellation
func TestDaemon_ContextCancellation(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	// Use 'sleep' so agent doesn't exit immediately
	cmd := exec.Command("sleep", "60")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	var daemonErr error
	go func() {
		daemonErr = RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// Verify daemon is running
	session, err := mgr.GetSession(wsID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if session.PID == 0 {
		t.Fatal("expected PID to be set")
	}

	// Verify process is alive
	if !process.Alive(session.PID) {
		t.Fatal("expected agent process to be alive")
	}

	// Cancel context
	cancel()

	// Wait for daemon to exit
	select {
	case <-daemonDone:
		// Success
	case <-time.After(8 * time.Second):
		t.Fatal("daemon did not exit after context cancellation")
	}

	// Verify process was terminated
	time.Sleep(100 * time.Millisecond)
	if process.Alive(session.PID) {
		t.Error("expected agent process to be terminated")
	}

	if daemonErr != nil {
		t.Logf("daemon error (expected on cancellation): %v", daemonErr)
	}
}

// TestDaemon_SocketCleanup tests that socket is cleaned up on daemon exit
func TestDaemon_SocketCleanup(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("sh", "-c", "exit 0")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	// Wait for daemon to start and exit
	<-daemonDone

	// Verify socket was cleaned up
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("expected socket to be cleaned up, but it still exists")
	}
}

// TestDaemon_InvalidConfig tests error handling for invalid configuration
func TestDaemon_InvalidConfig(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	ctx := context.Background()

	tests := []struct {
		name   string
		config DaemonConfig
		errMsg string
	}{
		{
			name: "missing workspace ID",
			config: DaemonConfig{
				SocketPath: "/tmp/test.sock",
				AgentCmd:   exec.Command("cat"),
				Manager:    mgr,
			},
			errMsg: "workspace ID is required",
		},
		{
			name: "missing socket path",
			config: DaemonConfig{
				WorkspaceID: wsID,
				AgentCmd:    exec.Command("cat"),
				Manager:     mgr,
			},
			errMsg: "socket path is required",
		},
		{
			name: "missing agent command",
			config: DaemonConfig{
				WorkspaceID: wsID,
				SocketPath:  "/tmp/test.sock",
				Manager:     mgr,
			},
			errMsg: "agent command is required",
		},
		{
			name: "missing manager",
			config: DaemonConfig{
				WorkspaceID: wsID,
				SocketPath:  "/tmp/test.sock",
				AgentCmd:    exec.Command("cat"),
			},
			errMsg: "workspace manager is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunDaemon(ctx, tt.config)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("expected error to contain %q, got %q", tt.errMsg, err.Error())
			}
		})
	}

	_ = tmpDir // avoid unused warning
}

// TestDaemon_MultipleInputOutput tests multiple input/output cycles
func TestDaemon_MultipleInputOutput(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    1024,
		Manager:     mgr,
	}

	daemonDone := make(chan struct{})
	go func() {
		RunDaemon(ctx, cfg)
		close(daemonDone)
	}()

	time.Sleep(100 * time.Millisecond)

	conn, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	sendAttach(t, conn)

	scanner := bufio.NewScanner(conn)
	// Skip MsgReady and MsgBuffer
	scanner.Scan()
	scanner.Scan()

	// Send multiple inputs
	inputs := []string{"line1\n", "line2\n", "line3\n"}
	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, input := range inputs {
			msg := Msg{
				Type: MsgInput,
				Data: EncodeData([]byte(input)),
			}
			encoded, _ := Encode(msg)
			conn.Write(encoded)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Reader goroutine
	received := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for received < len(inputs) && scanner.Scan() {
			msg, err := Decode(scanner.Bytes())
			if err != nil {
				continue
			}
			if msg.Type == MsgOutput {
				received++
			}
		}
	}()

	// Wait for I/O to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("I/O did not complete within timeout")
	}

	if received != len(inputs) {
		t.Errorf("expected %d outputs, got %d", len(inputs), received)
	}

	cancel()
	<-daemonDone
}
