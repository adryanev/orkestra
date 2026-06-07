//go:build !windows

package pty

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/adryanev/orkestra/pkg/audit"
	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/state"
	"github.com/adryanev/orkestra/pkg/workspace"
)

// DaemonConfig holds configuration for the PTY broker daemon.
type DaemonConfig struct {
	WorkspaceID      string             // Workspace identifier
	SocketPath       string             // Unix socket path for client connections
	AgentCmd         *exec.Cmd          // Agent command (not started yet)
	RingSize         int                // Ring buffer size in bytes
	Manager          *workspace.Manager // Workspace manager for persistence
	ApprovalTimeout  time.Duration      // Timeout for approval requests (default: 5 minutes)
	EnableAutoApprove bool              // Enable automatic approval interception (default: false)
}

// PTYSession tracks the PTY master and agent process information.
type PTYSession struct {
	MasterFd     *os.File
	PID          int
	PGID         int
	StartedAt    int64
	SocketPath   string
}

// RunDaemon starts the PTY broker daemon that manages a single agent PTY session.
// It allocates a PTY pair, starts the agent with the slave, persists the session,
// creates a Unix socket for client connections, maintains a ring buffer of output,
// and handles client attach/detach/input/resize operations.
func RunDaemon(ctx context.Context, cfg DaemonConfig) error {
	if cfg.WorkspaceID == "" {
		return fmt.Errorf("workspace ID is required")
	}
	if cfg.SocketPath == "" {
		return fmt.Errorf("socket path is required")
	}
	if cfg.AgentCmd == nil {
		return fmt.Errorf("agent command is required")
	}
	if cfg.Manager == nil {
		return fmt.Errorf("workspace manager is required")
	}
	if cfg.RingSize <= 0 {
		cfg.RingSize = 64 * 1024 // default to 64KB
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = 5 * time.Minute // default to 5 minutes
	}

	// 1. Allocate PTY pair
	master, slave, err := Open()
	if err != nil {
		return fmt.Errorf("failed to allocate PTY: %w", err)
	}
	defer master.Close()

	// 2. Set agent command stdio to slave fd
	cfg.AgentCmd.Stdin = slave
	cfg.AgentCmd.Stdout = slave
	cfg.AgentCmd.Stderr = slave

	// 3. Set agent SysProcAttr for process group
	// Create new session so the process doesn't receive SIGHUP when slave closes
	cfg.AgentCmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session (implies new process group)
	}

	// 4. Start agent and capture PID immediately
	if err := cfg.AgentCmd.Start(); err != nil {
		slave.Close()
		return fmt.Errorf("failed to start agent: %w", err)
	}

	pid := cfg.AgentCmd.Process.Pid
	pgid := pid // With Setsid, PGID equals PID

	// Debug: log process start
	fmt.Fprintf(os.Stderr, "[daemon] started agent PID=%d PGID=%d\n", pid, pgid)

	// 5. Close slave in daemon (agent has its own copy)
	slave.Close()

	// 6. Capture process start time for identity tracking
	startedAt, err := process.StartedAt(pid)
	if err != nil {
		_ = process.TerminateGroup(pgid, process.DefaultGrace)
		_ = cfg.AgentCmd.Wait()
		return fmt.Errorf("failed to capture agent start time: %w", err)
	}

	// 7. Persist PTY session via Manager
	session := PTYSession{
		MasterFd:   master,
		PID:        pid,
		PGID:       pgid,
		StartedAt:  startedAt,
		SocketPath: cfg.SocketPath,
	}

	if err := cfg.Manager.SetSessionProcess(cfg.WorkspaceID, "", "", pid, pgid, startedAt); err != nil {
		_ = process.TerminateGroup(pgid, process.DefaultGrace)
		_ = cfg.AgentCmd.Wait()
		return fmt.Errorf("failed to persist PTY session: %w", err)
	}

	// 8. Create Unix socket
	// Remove stale socket if it exists
	_ = os.Remove(cfg.SocketPath)

	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		_ = process.TerminateGroup(pgid, process.DefaultGrace)
		_ = cfg.AgentCmd.Wait()
		return fmt.Errorf("failed to create Unix socket: %w", err)
	}
	defer func() {
		listener.Close()
		_ = os.Remove(cfg.SocketPath)
	}()

	// 9. Initialize ring buffer
	ring := NewRingBuffer(cfg.RingSize)

	// 10. Set up daemon context
	daemonCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var clientMu sync.Mutex
	var currentClient net.Conn

	// Goroutine A: PTY reader → prompt detection → ring buffer → current client
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Create a line buffer for prompt detection
		var lineBuffer bytes.Buffer
		buf := make([]byte, 4096)

		for {
			n, err := master.Read(buf)
			if n > 0 {
				data := buf[:n]

				// Process data for prompt detection if enabled
				if cfg.EnableAutoApprove {
					processedData := processWithPromptDetection(
						data,
						&lineBuffer,
						cfg.WorkspaceID,
						cfg.ApprovalTimeout,
						master,
					)

					// Write processed data (original or modified) to ring buffer
					ring.Write(processedData)

					// Send to current client if attached
					clientMu.Lock()
					if currentClient != nil {
						msg := Msg{
							Type: MsgOutput,
							Data: EncodeData(processedData),
						}
						encoded, encErr := Encode(msg)
						if encErr == nil {
							_, _ = currentClient.Write(encoded)
						}
					}
					clientMu.Unlock()
				} else {
					// No prompt detection - pass through
					ring.Write(data)

					// Send to current client if attached
					clientMu.Lock()
					if currentClient != nil {
						msg := Msg{
							Type: MsgOutput,
							Data: EncodeData(data),
						}
						encoded, encErr := Encode(msg)
						if encErr == nil {
							_, _ = currentClient.Write(encoded)
						}
					}
					clientMu.Unlock()
				}
			}
			if err != nil {
				if err != io.EOF {
					// Log error but continue
				}
				return
			}
		}
	}()

	// Goroutine B: AgentCmd.Wait() → MsgExit → cancel context
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr := cfg.AgentCmd.Wait()

		exitCode := 0
		if waitErr != nil {
			// Debug: log the wait error
			fmt.Fprintf(os.Stderr, "[daemon] agent wait error: %v\n", waitErr)
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		// Send exit message to current client
		clientMu.Lock()
		if currentClient != nil {
			msg := Msg{
				Type: MsgExit,
				Code: &exitCode,
			}
			encoded, encErr := Encode(msg)
			if encErr == nil {
				_, _ = currentClient.Write(encoded)
			}
		}
		clientMu.Unlock()

		// Clear session process from manager
		_ = cfg.Manager.ClearSessionProcess(cfg.WorkspaceID)

		// Cancel daemon context to stop accept loop
		cancel()
	}()

	// Accept loop: handle client connections
	acceptDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(acceptDone)

		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-daemonCtx.Done():
					return
				default:
					// Log error and continue
					continue
				}
			}

			// Handle client connection in a separate goroutine
			// so the accept loop can continue accepting new connections
			go handleClient(daemonCtx, conn, &session, ring, &clientMu, &currentClient, master)
		}
	}()

	// Wait for daemon context cancellation or external context
	select {
	case <-daemonCtx.Done():
		// Agent exited
		fmt.Fprintf(os.Stderr, "[daemon] agent exited, shutting down\n")
	case <-ctx.Done():
		// External cancellation
		fmt.Fprintf(os.Stderr, "[daemon] context canceled, terminating agent\n")
		_ = process.TerminateGroup(pgid, process.DefaultGrace)
	}

	// Close listener to stop accept loop
	listener.Close()

	// Wait for all goroutines
	wg.Wait()

	return nil
}

// handleClient manages a single client connection.
func handleClient(
	ctx context.Context,
	conn net.Conn,
	session *PTYSession,
	ring *RingBuffer,
	clientMu *sync.Mutex,
	currentClient *net.Conn,
	master *os.File,
) {
	defer conn.Close()

	// Check if another client is already attached
	clientMu.Lock()
	if *currentClient != nil {
		// Reject concurrent attach
		msg := Msg{
			Type:    MsgError,
			Message: "another client is already attached",
		}
		encoded, _ := Encode(msg)
		conn.Write(encoded)
		clientMu.Unlock()
		return
	}
	*currentClient = conn
	clientMu.Unlock()

	defer func() {
		clientMu.Lock()
		if *currentClient == conn {
			*currentClient = nil
		}
		clientMu.Unlock()
	}()

	// Send MsgReady
	readyMsg := Msg{
		Type:    MsgReady,
		Message: "attached to PTY session",
	}
	encoded, err := Encode(readyMsg)
	if err != nil {
		return
	}
	if _, err := conn.Write(encoded); err != nil {
		return
	}

	// Send MsgBuffer with ring buffer contents
	bufferData := ring.Bytes()
	if len(bufferData) > 0 {
		bufferMsg := Msg{
			Type: MsgBuffer,
			Data: EncodeData(bufferData),
		}
		encoded, err := Encode(bufferMsg)
		if err == nil {
			_, _ = conn.Write(encoded)
		}
	}

	// Input/resize/detach loop
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		msg, err := Decode(line)
		if err != nil {
			continue
		}

		switch msg.Type {
		case MsgInput:
			// Write input to PTY master
			data, err := DecodeData(msg.Data)
			if err != nil {
				continue
			}
			_, _ = master.Write(data)

		case MsgResize:
			// Resize PTY
			if msg.Rows > 0 && msg.Cols > 0 {
				_ = Setsize(master, msg.Rows, msg.Cols)
			}

		case MsgDetach:
			// Client detaching
			return

		default:
			// Unknown message type, ignore
		}
	}
}

// processWithPromptDetection processes PTY output data, buffering lines to detect
// permission prompts. When a prompt is detected, it triggers the approval workflow
// and injects the response back to the PTY.
func processWithPromptDetection(
	data []byte,
	lineBuffer *bytes.Buffer,
	workspaceID string,
	timeout time.Duration,
	master *os.File,
) []byte {
	// Buffer the incoming data
	lineBuffer.Write(data)

	// Check if we have complete lines (ending with newline)
	bufferContent := lineBuffer.String()

	// Split into lines, keeping the last incomplete line in buffer
	lines := strings.Split(bufferContent, "\n")
	if len(lines) == 0 {
		return data
	}

	// Keep the last incomplete line in buffer
	lastLine := lines[len(lines)-1]
	lineBuffer.Reset()
	lineBuffer.WriteString(lastLine)

	// Process complete lines for prompt detection
	for i := 0; i < len(lines)-1; i++ {
		line := lines[i]
		if detected, agent, command := DetectPrompt(line); detected {
			// Prompt detected - trigger approval workflow
			handleApprovalRequest(workspaceID, agent, command, timeout, master)
		}
	}

	// Also check the incomplete line for prompts (some prompts don't end with newline)
	if lastLine != "" {
		if detected, agent, command := DetectPrompt(lastLine); detected {
			// Prompt detected - trigger approval workflow
			handleApprovalRequest(workspaceID, agent, command, timeout, master)
			// Clear the buffer since we processed the prompt
			lineBuffer.Reset()
		}
	}

	// Return original data unchanged (response injection happens separately)
	return data
}

// handleApprovalRequest orchestrates the approval workflow:
// 1. Classify risk
// 2. Create approval request
// 3. Write to pending state
// 4. Wait for response (with timeout)
// 5. Inject approval/rejection to PTY
// 6. Cleanup state
// 7. Audit log
func handleApprovalRequest(
	workspaceID string,
	agent string,
	command string,
	timeout time.Duration,
	master *os.File,
) {
	// 1. Classify risk
	riskLevel := ClassifyRisk(command)

	// 2. Create approval request
	req := state.NewApprovalRequest(workspaceID, agent, command, riskLevel, timeout)

	// 3. Write to pending state
	if err := state.WritePendingApproval(workspaceID, req); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to write pending approval: %v\n", err)
		// On error, auto-reject
		injectResponse(master, false)
		return
	}

	// 4. Audit log the request
	if err := audit.LogRequest(workspaceID, agent, command, req.ID, riskLevel); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to audit log request: %v\n", err)
	}

	// 5. Wait for response with timeout
	response := waitForApprovalResponse(workspaceID, timeout)

	// 6. Handle response
	if response == nil {
		// Timeout - auto-reject
		fmt.Fprintf(os.Stderr, "[daemon] approval timeout for command: %s\n", command)
		injectResponse(master, false)

		// Audit log timeout
		if err := audit.LogTimeout(workspaceID, agent, command, req.ID, riskLevel); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] failed to audit log timeout: %v\n", err)
		}
	} else {
		// Response received
		fmt.Fprintf(os.Stderr, "[daemon] approval response: approved=%v for command: %s\n", response.Approved, command)
		injectResponse(master, response.Approved)

		// Audit log response
		if response.Approved {
			if err := audit.LogApprove(workspaceID, agent, command, req.ID, response.RespondedBy, riskLevel); err != nil {
				fmt.Fprintf(os.Stderr, "[daemon] failed to audit log approval: %v\n", err)
			}
		} else {
			if err := audit.LogReject(workspaceID, agent, command, req.ID, response.RespondedBy, riskLevel); err != nil {
				fmt.Fprintf(os.Stderr, "[daemon] failed to audit log rejection: %v\n", err)
			}
		}
	}

	// 7. Cleanup approval state
	if err := state.CleanupApprovalState(workspaceID); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to cleanup approval state: %v\n", err)
	}
}

// waitForApprovalResponse polls for an approval response with timeout.
// Returns nil on timeout, otherwise returns the response.
func waitForApprovalResponse(workspaceID string, timeout time.Duration) *state.ApprovalResponse {
	deadline := time.Now().Add(timeout)
	pollInterval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		response, err := state.ReadApprovalResponse(workspaceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] error reading approval response: %v\n", err)
			time.Sleep(pollInterval)
			continue
		}

		if response != nil {
			return response
		}

		// Sleep before next poll
		time.Sleep(pollInterval)
	}

	// Timeout
	return nil
}

// injectResponse injects an approval response ("y" or "n") to the PTY master stdin.
func injectResponse(master *os.File, approved bool) {
	var response string
	if approved {
		response = "y\n"
	} else {
		response = "n\n"
	}

	if _, err := master.Write([]byte(response)); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to inject approval response: %v\n", err)
	}
}
