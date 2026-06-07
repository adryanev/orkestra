//go:build !windows

package pty

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	// Detach sequence: Ctrl+\ followed by Ctrl+_
	detachByte1 = 0x1C // Ctrl+\ (FS - File Separator)
	detachByte2 = 0x1F // Ctrl+_ (US - Unit Separator)
)

// DetachDetector is a two-byte state machine for detecting the detach sequence.
// It transitions through states: idle → seenFirst → idle (on match or mismatch).
type DetachDetector struct {
	seenFirst bool
}

// NewDetachDetector creates a new detach sequence detector.
func NewDetachDetector() *DetachDetector {
	return &DetachDetector{}
}

// Feed processes a single byte and returns true if the detach sequence is complete.
// The detector consumes both bytes of the sequence when matched, returning false
// for the first byte and true for the second byte.
func (d *DetachDetector) Feed(b byte) bool {
	if d.seenFirst {
		if b == detachByte2 {
			d.seenFirst = false
			return true // Complete detach sequence detected
		}
		// Not the second byte - check if it's another first byte
		if b == detachByte1 {
			// Keep seenFirst = true, waiting for second byte
			return false
		}
		// Some other byte - reset state
		d.seenFirst = false
		return false
	}
	if b == detachByte1 {
		d.seenFirst = true
		return false
	}
	return false
}

// Reset clears the detector state.
func (d *DetachDetector) Reset() {
	d.seenFirst = false
}

// RunAttach connects to a PTY daemon socket and attaches the client's terminal.
// It handles:
//   - Connection establishment with retry
//   - Terminal raw mode setup
//   - Bidirectional data flow (stdin → socket, socket → stdout)
//   - Detach sequence detection (Ctrl+\ Ctrl+_)
//   - Window resize signals (SIGWINCH)
//   - Clean shutdown on detach, exit, or error
func RunAttach(ctx context.Context, socketPath string, stdinFd int, stdout io.Writer) error {
	if socketPath == "" {
		return fmt.Errorf("socket path is required")
	}

	// Connect to socket with retry
	conn, err := connectWithRetry(ctx, socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to PTY daemon: %w", err)
	}
	defer conn.Close()

	// Get current terminal size
	rows, cols, err := CurrentSize(uintptr(stdinFd))
	if err != nil {
		// Non-fatal: daemon will use default size
		rows, cols = 24, 80
	}

	// Send MsgAttach
	attachMsg := Msg{
		Type: MsgAttach,
		Rows: rows,
		Cols: cols,
	}
	encoded, err := Encode(attachMsg)
	if err != nil {
		return fmt.Errorf("failed to encode attach message: %w", err)
	}
	if _, err := conn.Write(encoded); err != nil {
		return fmt.Errorf("failed to send attach message: %w", err)
	}

	// Receive MsgReady and MsgBuffer
	scanner := bufio.NewScanner(conn)

	// Read MsgReady
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read ready message: %w", err)
		}
		return fmt.Errorf("daemon closed connection before sending ready")
	}
	readyMsg, err := Decode(scanner.Bytes())
	if err != nil {
		return fmt.Errorf("failed to decode ready message: %w", err)
	}
	if readyMsg.Type == MsgError {
		return fmt.Errorf("daemon error: %s", readyMsg.Message)
	}
	if readyMsg.Type != MsgReady {
		return fmt.Errorf("expected ready message, got %s", readyMsg.Type)
	}

	// Read MsgBuffer (optional, may not be present if buffer is empty)
	// We need to peek ahead without blocking, but scanner.Scan() blocks.
	// Instead, we'll start the socket reader goroutine and let it handle
	// both MsgBuffer and subsequent messages.

	// Set terminal to raw mode
	oldState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return fmt.Errorf("failed to set terminal to raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(stdinFd, oldState)
	}()

	// Context for goroutines
	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errChan := make(chan error, 3)

	// Goroutine A: stdin → DetachDetector → MsgInput/MsgDetach
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel() // Signal other goroutines on exit

		detector := NewDetachDetector()
		stdin := os.NewFile(uintptr(stdinFd), "/dev/stdin")
		buf := make([]byte, 1024)

		for {
			select {
			case <-attachCtx.Done():
				return
			default:
			}

			n, err := stdin.Read(buf)
			if n > 0 {
				// Scan for detach sequence
				var filtered []byte
				for i := 0; i < n; i++ {
					if detector.Feed(buf[i]) {
						// Detach sequence complete
						detachMsg := Msg{Type: MsgDetach}
						encoded, encErr := Encode(detachMsg)
						if encErr == nil {
							_, _ = conn.Write(encoded)
						}
						return
					}
					// Only include non-first-byte or reset bytes
					if !detector.seenFirst || buf[i] != detachByte1 {
						filtered = append(filtered, buf[i])
					}
				}

				// Send filtered input to daemon
				if len(filtered) > 0 {
					inputMsg := Msg{
						Type: MsgInput,
						Data: EncodeData(filtered),
					}
					encoded, encErr := Encode(inputMsg)
					if encErr != nil {
						errChan <- fmt.Errorf("failed to encode input: %w", encErr)
						return
					}
					if _, writeErr := conn.Write(encoded); writeErr != nil {
						errChan <- fmt.Errorf("failed to send input: %w", writeErr)
						return
					}
				}
			}
			if err != nil {
				if err != io.EOF {
					errChan <- fmt.Errorf("stdin read error: %w", err)
				}
				return
			}
		}
	}()

	// Goroutine B: socket → MsgOutput/MsgBuffer/MsgExit/MsgError → stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()

		// Continue reading from the already-started scanner
		for {
			select {
			case <-attachCtx.Done():
				return
			default:
			}

			if !scanner.Scan() {
				if err := scanner.Err(); err != nil && err != io.EOF {
					errChan <- fmt.Errorf("socket read error: %w", err)
				}
				return
			}

			msg, err := Decode(scanner.Bytes())
			if err != nil {
				// Ignore malformed messages
				continue
			}

			switch msg.Type {
			case MsgBuffer:
				// Initial buffer history
				data, decErr := DecodeData(msg.Data)
				if decErr == nil && len(data) > 0 {
					_, _ = stdout.Write(data)
				}

			case MsgOutput:
				// Live PTY output
				data, decErr := DecodeData(msg.Data)
				if decErr == nil && len(data) > 0 {
					_, _ = stdout.Write(data)
				}

			case MsgExit:
				// PTY process exited
				return

			case MsgError:
				errChan <- fmt.Errorf("daemon error: %s", msg.Message)
				return
			}
		}
	}()

	// SIGWINCH handler → MsgResize
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer signal.Stop(sigChan)

		for {
			select {
			case <-attachCtx.Done():
				return
			case <-sigChan:
				// Terminal was resized
				newRows, newCols, err := CurrentSize(uintptr(stdinFd))
				if err != nil {
					continue
				}
				resizeMsg := Msg{
					Type: MsgResize,
					Rows: newRows,
					Cols: newCols,
				}
				encoded, encErr := Encode(resizeMsg)
				if encErr != nil {
					continue
				}
				_, _ = conn.Write(encoded)
			}
		}
	}()

	// Wait for goroutines to complete
	wg.Wait()

	// Check for errors
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

// connectWithRetry attempts to connect to a Unix socket with exponential backoff.
func connectWithRetry(ctx context.Context, socketPath string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	backoff := 50 * time.Millisecond

	for {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			return conn, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect timeout: %w", err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			if backoff*2 < 500*time.Millisecond {
				backoff = backoff * 2
			} else {
				backoff = 500 * time.Millisecond
			}
		}
	}
}
