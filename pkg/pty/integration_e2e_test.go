//go:build !windows

package pty

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegration_DaemonClientEcho tests the complete flow:
// 1. Start daemon with 'cat' agent (echoes input)
// 2. Attach client via socket
// 3. Send input message
// 4. Verify output message contains the echoed data
func TestIntegration_DaemonClientEcho(t *testing.T) {
	// Setup test workspace
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "integration-test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use 'cat' as echo agent
	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    4096,
		Manager:     mgr,
	}

	// Start daemon in background
	daemonDone := make(chan struct{})
	daemonStarted := make(chan error, 1)
	go func() {
		defer close(daemonDone)
		err := RunDaemon(ctx, cfg)
		if err != nil {
			select {
			case daemonStarted <- err:
			default:
			}
		}
	}()

	// Give daemon time to start and create socket
	select {
	case err := <-daemonStarted:
		t.Fatalf("daemon failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Daemon started successfully
	}

	// Verify socket exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatalf("daemon socket not created at %s", socketPath)
	}

	// Connect client to daemon
	conn, err := connectToDaemon(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to daemon: %v", err)
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	sendAttach(t, conn)

	// Step 1: Expect MsgReady from daemon
	if !scanner.Scan() {
		t.Fatal("expected MsgReady but connection closed")
	}
	readyMsg, err := Decode(scanner.Bytes())
	if err != nil {
		t.Fatalf("failed to decode MsgReady: %v", err)
	}
	if readyMsg.Type != MsgReady {
		t.Errorf("expected MsgReady, got %s", readyMsg.Type)
	}
	t.Logf("✓ Received MsgReady: %s", readyMsg.Message)

	// Step 2: Daemon sends MsgBuffer only if ring buffer has data
	// Since this is a fresh session, buffer might be empty and message not sent
	// We need to check what the next message is - could be MsgBuffer or the first output
	t.Logf("✓ Checking for optional MsgBuffer or waiting for input...")

	// Step 3: Send input to daemon
	testInput := "hello integration test\n"
	inputMsg := Msg{
		Type: MsgInput,
		Data: EncodeData([]byte(testInput)),
	}
	encoded, err := Encode(inputMsg)
	if err != nil {
		t.Fatalf("failed to encode input message: %v", err)
	}
	if _, err := conn.Write(encoded); err != nil {
		t.Fatalf("failed to write input to socket: %v", err)
	}
	t.Logf("✓ Sent MsgInput: %q", testInput)

	// Step 4: Expect MsgOutput with echoed data
	outputReceived := false
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) && !outputReceived {
		conn.SetReadDeadline(deadline)
		if !scanner.Scan() {
			if scanner.Err() != nil {
				t.Fatalf("scanner error while waiting for output: %v", scanner.Err())
			}
			break
		}

		msg, err := Decode(scanner.Bytes())
		if err != nil {
			t.Logf("decode error (skipping): %v", err)
			continue
		}

		if msg.Type == MsgOutput {
			outputData, err := DecodeData(msg.Data)
			if err != nil {
				t.Fatalf("failed to decode output data: %v", err)
			}

			outputStr := string(outputData)
			t.Logf("✓ Received MsgOutput: %q", outputStr)

			// Verify the echoed data contains our input
			// Note: PTY converts \n to \r\n, and cat in a PTY may produce duplicate echoes
			// We just verify that our input appears in the output
			normalizedOutput := strings.ReplaceAll(outputStr, "\r\n", "\n")
			if !strings.Contains(normalizedOutput, testInput) {
				t.Errorf("output does not contain input: got %q, want to contain %q", normalizedOutput, testInput)
			}
			outputReceived = true
			break
		}
	}

	if !outputReceived {
		t.Fatal("did not receive expected MsgOutput within timeout")
	}

	// Step 5: Send detach message
	detachMsg := Msg{Type: MsgDetach}
	encoded, _ = Encode(detachMsg)
	conn.Write(encoded)
	t.Logf("✓ Sent MsgDetach")

	// Step 6: Close connection
	conn.Close()
	t.Logf("✓ Closed client connection")

	// Step 7: Stop daemon by canceling context
	cancel()

	// Wait for daemon to exit
	select {
	case <-daemonDone:
		t.Logf("✓ Daemon exited cleanly")
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit within timeout after context cancellation")
	}

	// Verify socket was cleaned up
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket was not cleaned up after daemon exit")
	}
	t.Logf("✓ Socket cleaned up")
}

// TestIntegration_MultipleInputOutputCycles tests multiple rounds of input/output
func TestIntegration_MultipleInputOutputCycles(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "multi-cycle-test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    4096,
		Manager:     mgr,
	}

	// Start daemon
	daemonDone := make(chan struct{})
	daemonStarted := make(chan error, 1)
	go func() {
		defer close(daemonDone)
		err := RunDaemon(ctx, cfg)
		if err != nil {
			select {
			case daemonStarted <- err:
			default:
			}
		}
	}()

	// Wait for daemon to start
	select {
	case err := <-daemonStarted:
		t.Fatalf("daemon failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Daemon started successfully
	}

	// Connect client
	conn, err := connectToDaemon(socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	sendAttach(t, conn)

	// Consume initial messages (MsgReady and optional MsgBuffer)
	initialMsgsRead := 0
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for scanner.Scan() && initialMsgsRead < 2 {
		msg, err := Decode(scanner.Bytes())
		if err != nil {
			continue
		}
		if msg.Type == MsgReady || msg.Type == MsgBuffer {
			initialMsgsRead++
		}
		if msg.Type == MsgReady {
			// After ready, wait a bit for potential buffer message
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Clear deadline and scanner error for actual test
	conn.SetReadDeadline(time.Time{})
	if scanner.Err() != nil {
		scanner = bufio.NewScanner(conn)
	}

	// Test multiple input/output cycles
	testCases := []string{
		"first line\n",
		"second line\n",
		"third line\n",
	}

	for i, input := range testCases {
		// Send input
		inputMsg := Msg{
			Type: MsgInput,
			Data: EncodeData([]byte(input)),
		}
		encoded, _ := Encode(inputMsg)
		if _, err := conn.Write(encoded); err != nil {
			t.Fatalf("failed to write input %d: %v", i, err)
		}
		t.Logf("Sent input %d: %q", i, input)

		// Wait for output
		outputReceived := false
		deadline := time.Now().Add(1 * time.Second)

		for time.Now().Before(deadline) && !outputReceived {
			conn.SetReadDeadline(deadline)
			if !scanner.Scan() {
				break
			}

			msg, err := Decode(scanner.Bytes())
			if err != nil {
				continue
			}

			if msg.Type == MsgOutput {
				outputData, _ := DecodeData(msg.Data)
				outputStr := string(outputData)

				// Normalize and check if output contains our input
				normalizedOutput := strings.ReplaceAll(outputStr, "\r\n", "\n")
				if strings.Contains(normalizedOutput, input) {
					t.Logf("✓ Received output %d: %q", i, outputStr)
					outputReceived = true
					break
				}
			}
		}

		if !outputReceived {
			t.Errorf("did not receive output for input %d", i)
		}
	}

	// Clean up
	detachMsg := Msg{Type: MsgDetach}
	encoded, _ := Encode(detachMsg)
	conn.Write(encoded)
	conn.Close()

	cancel()
	<-daemonDone
}

// TestIntegration_ClientReconnect tests that a second client can reconnect
// after the first client detaches and receive buffered history
func TestIntegration_ClientReconnect(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "reconnect-test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.Command("cat")
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    4096,
		Manager:     mgr,
	}

	// Start daemon
	daemonDone := make(chan struct{})
	daemonStarted := make(chan error, 1)
	go func() {
		defer close(daemonDone)
		err := RunDaemon(ctx, cfg)
		if err != nil {
			select {
			case daemonStarted <- err:
			default:
			}
		}
	}()

	// Wait for daemon to start
	select {
	case err := <-daemonStarted:
		t.Fatalf("daemon failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Daemon started successfully
	}

	// First client: connect and send data
	conn1, err := connectToDaemon(socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("failed to connect first client: %v", err)
	}

	scanner1 := bufio.NewScanner(conn1)
	sendAttach(t, conn1)
	// Read MsgReady and optional MsgBuffer
	scanner1.Scan() // MsgReady
	conn1.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	scanner1.Scan() // MsgBuffer or timeout
	conn1.SetReadDeadline(time.Time{})

	// Send test data that should be buffered
	testData := "data that should be buffered\n"
	inputMsg := Msg{
		Type: MsgInput,
		Data: EncodeData([]byte(testData)),
	}
	encoded, _ := Encode(inputMsg)
	conn1.Write(encoded)

	// Wait for output confirmation
	deadline := time.Now().Add(1 * time.Second)
	conn1.SetReadDeadline(deadline)
	scanner1.Scan()

	// Detach first client
	detachMsg := Msg{Type: MsgDetach}
	encoded, _ = Encode(detachMsg)
	conn1.Write(encoded)
	conn1.Close()

	t.Logf("✓ First client detached")

	// Give daemon time to process detach
	time.Sleep(100 * time.Millisecond)

	// Second client: connect and verify buffer contains history
	conn2, err := connectToDaemon(socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("failed to connect second client: %v", err)
	}
	defer conn2.Close()

	scanner2 := bufio.NewScanner(conn2)
	sendAttach(t, conn2)

	// Skip MsgReady
	if !scanner2.Scan() {
		t.Fatal("expected MsgReady for second client")
	}

	// Expect MsgBuffer with history
	if !scanner2.Scan() {
		t.Fatal("expected MsgBuffer for second client")
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

	bufferStr := string(bufferData)
	normalizedBuffer := strings.ReplaceAll(bufferStr, "\r\n", "\n")
	if !strings.Contains(normalizedBuffer, testData) {
		t.Errorf("expected buffer to contain %q, got %q", testData, normalizedBuffer)
	}
	t.Logf("✓ Second client received buffered history: %q", bufferStr)

	// Clean up
	detachMsg = Msg{Type: MsgDetach}
	encoded, _ = Encode(detachMsg)
	conn2.Write(encoded)
	conn2.Close()

	cancel()
	<-daemonDone
}

// TestIntegration_AgentExitPropagation tests that agent exit is properly
// propagated to the connected client via MsgExit
func TestIntegration_AgentExitPropagation(t *testing.T) {
	mgr, tmpDir := setupTestManager(t)
	wsID := createTestWorkspace(t, mgr, tmpDir)

	socketPath := filepath.Join(tmpDir, "exit-test.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use a script that waits a bit then exits with a specific code
	// This gives us time to connect before it exits
	exitCode := 42
	cmd := exec.Command("sh", "-c", fmt.Sprintf("sleep 0.5 && exit %d", exitCode))
	cfg := DaemonConfig{
		WorkspaceID: wsID,
		SocketPath:  socketPath,
		AgentCmd:    cmd,
		RingSize:    4096,
		Manager:     mgr,
	}

	// Start daemon
	daemonDone := make(chan struct{})
	daemonStarted := make(chan error, 1)
	go func() {
		defer close(daemonDone)
		err := RunDaemon(ctx, cfg)
		if err != nil {
			select {
			case daemonStarted <- err:
			default:
			}
		}
	}()

	// Wait for daemon to start
	select {
	case err := <-daemonStarted:
		t.Fatalf("daemon failed to start: %v", err)
	case <-time.After(500 * time.Millisecond):
		// Daemon started successfully
	}

	// Connect client (must connect before sleep expires)
	conn, err := connectToDaemon(socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	sendAttach(t, conn)

	// Wait for messages - we expect MsgReady, possibly MsgBuffer, then MsgExit
	exitReceived := false
	receivedExitCode := 0
	deadline := time.Now().Add(3 * time.Second)

	conn.SetReadDeadline(deadline)
	for scanner.Scan() {
		msg, err := Decode(scanner.Bytes())
		if err != nil {
			t.Logf("decode error: %v", err)
			continue
		}

		t.Logf("Received message type: %s", msg.Type)

		if msg.Type == MsgExit {
			exitReceived = true
			if msg.Code != nil {
				receivedExitCode = *msg.Code
			}
			t.Logf("✓ Received MsgExit with code %d", receivedExitCode)
			break
		}
	}

	if !exitReceived {
		t.Fatal("did not receive MsgExit within timeout")
	}

	if receivedExitCode != exitCode {
		t.Errorf("exit code mismatch: got %d, want %d", receivedExitCode, exitCode)
	}

	// Daemon should exit soon after agent exits
	select {
	case <-daemonDone:
		t.Logf("✓ Daemon exited after agent exit")
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit after agent exit")
	}

	// Verify session was cleared
	session, err := mgr.GetSession(wsID)
	if err == nil {
		if session.PID != 0 || session.PGID != 0 {
			t.Errorf("expected session process cleared, got PID=%d PGID=%d", session.PID, session.PGID)
		}
	}
}
