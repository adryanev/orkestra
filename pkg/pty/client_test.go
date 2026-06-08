//go:build !windows

package pty

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/term"
)

func TestDetachDetector(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []bool // expected result for each byte
	}{
		{
			name:     "no detach sequence",
			input:    []byte("hello world"),
			expected: []bool{false, false, false, false, false, false, false, false, false, false, false},
		},
		{
			name:     "complete detach sequence",
			input:    []byte{detachByte1, detachByte2},
			expected: []bool{false, true},
		},
		{
			name:     "first byte only",
			input:    []byte{detachByte1},
			expected: []bool{false},
		},
		{
			name:     "first byte followed by wrong byte",
			input:    []byte{detachByte1, 'a'},
			expected: []bool{false, false},
		},
		{
			name:     "detach in middle of text",
			input:    []byte{'h', 'e', detachByte1, detachByte2, 'l', 'o'},
			expected: []bool{false, false, false, true, false, false},
		},
		{
			name:     "multiple first bytes",
			input:    []byte{detachByte1, detachByte1, detachByte2},
			expected: []bool{false, false, true},
		},
		{
			name:     "second byte without first",
			input:    []byte{detachByte2},
			expected: []bool{false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDetachDetector()
			for i, b := range tt.input {
				result := d.Feed(b)
				if result != tt.expected[i] {
					t.Errorf("byte %d (0x%02x): got %v, want %v", i, b, result, tt.expected[i])
				}
			}
		})
	}
}

func TestDetachDetectorReset(t *testing.T) {
	d := NewDetachDetector()

	// Feed first byte
	d.Feed(detachByte1)
	if !d.seenFirst {
		t.Fatal("expected seenFirst to be true")
	}

	// Reset
	d.Reset()
	if d.seenFirst {
		t.Fatal("expected seenFirst to be false after reset")
	}
}

// fakeDaemon simulates a PTY daemon for testing RunAttach.
type fakeDaemon struct {
	t          *testing.T
	listener   net.Listener
	clientConn net.Conn
	mu         sync.Mutex
	ready      chan struct{}
	done       chan struct{}
	// Test control
	sendBuffer    []byte
	sendOutput    [][]byte
	expectInput   [][]byte
	expectResize  []struct{ rows, cols uint16 }
	expectDetach  bool
	sendExit      bool
	sendExitCode  int
	errorOnAttach string
}

func newFakeDaemon(t *testing.T, socketPath string) *fakeDaemon {
	t.Helper()

	// Remove stale socket
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create test socket: %v", err)
	}

	fd := &fakeDaemon{
		t:        t,
		listener: listener,
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
	}

	go fd.acceptLoop()

	return fd
}

func (fd *fakeDaemon) acceptLoop() {
	// Signal ready once socket is listening
	close(fd.ready)

	conn, err := fd.listener.Accept()
	if err != nil {
		return
	}

	fd.mu.Lock()
	fd.clientConn = conn
	fd.mu.Unlock()

	go fd.handleClient(conn)
}

func (fd *fakeDaemon) handleClient(conn net.Conn) {
	defer close(fd.done)
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	// Read MsgAttach
	if !scanner.Scan() {
		fd.t.Errorf("failed to read attach message: %v", scanner.Err())
		return
	}

	msg, err := Decode(scanner.Bytes())
	if err != nil {
		fd.t.Errorf("failed to decode attach message: %v", err)
		return
	}

	if msg.Type != MsgAttach {
		fd.t.Errorf("expected MsgAttach, got %s", msg.Type)
		return
	}

	// Send error or ready
	if fd.errorOnAttach != "" {
		errMsg := Msg{Type: MsgError, Message: fd.errorOnAttach}
		encoded, _ := Encode(errMsg)
		conn.Write(encoded)
		return
	}

	readyMsg := Msg{Type: MsgReady, Message: "test ready"}
	encoded, _ := Encode(readyMsg)
	if _, err := conn.Write(encoded); err != nil {
		fd.t.Errorf("failed to send ready: %v", err)
		return
	}

	// Send buffer if configured
	if len(fd.sendBuffer) > 0 {
		bufMsg := Msg{Type: MsgBuffer, Data: EncodeData(fd.sendBuffer)}
		encoded, _ := Encode(bufMsg)
		conn.Write(encoded)
	}

	// Send output messages
	for _, data := range fd.sendOutput {
		outMsg := Msg{Type: MsgOutput, Data: EncodeData(data)}
		encoded, _ := Encode(outMsg)
		conn.Write(encoded)
		time.Sleep(10 * time.Millisecond) // Small delay to ensure proper ordering
	}

	// Process client messages
	inputIdx := 0
	resizeIdx := 0

	for scanner.Scan() {
		msg, err := Decode(scanner.Bytes())
		if err != nil {
			continue
		}

		switch msg.Type {
		case MsgInput:
			data, _ := DecodeData(msg.Data)
			if inputIdx < len(fd.expectInput) {
				expected := fd.expectInput[inputIdx]
				if string(data) != string(expected) {
					fd.t.Errorf("input %d: got %q, want %q", inputIdx, data, expected)
				}
				inputIdx++
			} else {
				fd.t.Errorf("unexpected input: %q", data)
			}

		case MsgResize:
			if resizeIdx < len(fd.expectResize) {
				expected := fd.expectResize[resizeIdx]
				if msg.Rows != expected.rows || msg.Cols != expected.cols {
					fd.t.Errorf("resize %d: got %dx%d, want %dx%d",
						resizeIdx, msg.Rows, msg.Cols, expected.rows, expected.cols)
				}
				resizeIdx++
			} else {
				fd.t.Errorf("unexpected resize: %dx%d", msg.Rows, msg.Cols)
			}

		case MsgDetach:
			if !fd.expectDetach {
				fd.t.Error("unexpected detach message")
			}
			return
		}
	}

	// Send exit if configured
	if fd.sendExit {
		exitMsg := Msg{Type: MsgExit, Code: &fd.sendExitCode}
		encoded, _ := Encode(exitMsg)
		conn.Write(encoded)
	}
}

func (fd *fakeDaemon) waitReady(timeout time.Duration) bool {
	select {
	case <-fd.ready:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (fd *fakeDaemon) close() {
	fd.listener.Close()
	if fd.clientConn != nil {
		fd.clientConn.Close()
	}
	<-fd.done
}

func TestRunAttach_Success(t *testing.T) {
	// RunAttach calls term.MakeRaw on stdinFd, which requires a real terminal.
	// In CI and unit-test environments stdin is not a TTY, so skip rather than
	// letting the test silently succeed without reaching the data-pump code.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires real terminal: stdin is not a TTY")
	}

	socketPath := "/tmp/test-pty-attach-success.sock"
	defer os.Remove(socketPath)

	daemon := newFakeDaemon(t, socketPath)
	daemon.sendBuffer = []byte("buffer content\n")
	daemon.sendOutput = [][]byte{
		[]byte("line 1\n"),
		[]byte("line 2\n"),
	}
	daemon.sendExit = true
	daemon.sendExitCode = 0

	if !daemon.waitReady(2 * time.Second) {
		t.Fatal("daemon not ready")
	}

	_, stdinWriter := io.Pipe()
	defer stdinWriter.Close()

	var outputBuf strings.Builder

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- RunAttach(ctx, socketPath, int(os.Stdin.Fd()), &outputBuf)
	}()

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("attach timeout")
	}

	daemon.close()
}

func TestRunAttach_DaemonError(t *testing.T) {
	socketPath := "/tmp/test-pty-attach-error.sock"
	defer os.Remove(socketPath)

	daemon := newFakeDaemon(t, socketPath)
	daemon.errorOnAttach = "test error message"

	if !daemon.waitReady(2 * time.Second) {
		t.Fatal("daemon not ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var outputBuf strings.Builder
	err := RunAttach(ctx, socketPath, int(os.Stdin.Fd()), &outputBuf)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "test error message") {
		t.Errorf("expected error to contain 'test error message', got: %v", err)
	}

	daemon.close()
}

func TestRunAttach_SocketNotFound(t *testing.T) {
	socketPath := "/tmp/nonexistent-socket.sock"

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var outputBuf strings.Builder
	err := RunAttach(ctx, socketPath, int(os.Stdin.Fd()), &outputBuf)

	if err == nil {
		t.Fatal("expected error for nonexistent socket, got nil")
	}

	if !strings.Contains(err.Error(), "connect timeout") &&
		!strings.Contains(err.Error(), "no such file") &&
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunAttach_EmptySocketPath(t *testing.T) {
	ctx := context.Background()
	var outputBuf strings.Builder

	err := RunAttach(ctx, "", int(os.Stdin.Fd()), &outputBuf)

	if err == nil {
		t.Fatal("expected error for empty socket path, got nil")
	}

	if !strings.Contains(err.Error(), "socket path is required") {
		t.Errorf("expected 'socket path is required' error, got: %v", err)
	}
}

func TestConnectWithRetry(t *testing.T) {
	socketPath := "/tmp/test-pty-connect-retry.sock"
	defer os.Remove(socketPath)

	ctx := context.Background()

	// Start listening after a delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return
		}
		defer listener.Close()
		conn, _ := listener.Accept()
		if conn != nil {
			conn.Close()
		}
	}()

	// Should retry and eventually connect
	conn, err := connectWithRetry(ctx, socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("connectWithRetry failed: %v", err)
	}
	conn.Close()
}

func TestConnectWithRetry_Timeout(t *testing.T) {
	socketPath := "/tmp/nonexistent-connect-retry.sock"

	ctx := context.Background()

	// Should timeout since socket never appears
	_, err := connectWithRetry(ctx, socketPath, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !strings.Contains(err.Error(), "connect timeout") {
		t.Errorf("expected connect timeout error, got: %v", err)
	}
}

func TestConnectWithRetry_ContextCanceled(t *testing.T) {
	socketPath := "/tmp/nonexistent-cancel.sock"

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := connectWithRetry(ctx, socketPath, 5*time.Second)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}
