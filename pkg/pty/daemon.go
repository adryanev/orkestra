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
	WorkspaceID       string             // Workspace identifier
	SocketPath        string             // Unix socket path for client connections
	AgentCmd          *exec.Cmd          // Agent command (not started yet)
	RingSize          int                // Ring buffer size in bytes
	Manager           *workspace.Manager // Workspace manager for persistence
	ApprovalTimeout   time.Duration      // Timeout for approval requests (default: 5 minutes)
	EnableAutoApprove bool               // Enable automatic approval interception (default: false)
}

// PTYSession tracks the PTY master and agent process information.
type PTYSession struct {
	MasterFd   *os.File
	PID        int
	PGID       int
	StartedAt  int64
	SocketPath string
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

	configDir := cfg.Manager.ConfigDir()

	daemonPID := os.Getpid()
	daemonPGID, err := process.PGID(daemonPID)
	if err != nil {
		return fmt.Errorf("failed to capture PTY daemon process group: %w", err)
	}
	daemonStart, err := process.StartedAt(daemonPID)
	if err != nil {
		return fmt.Errorf("failed to capture PTY daemon identity: %w", err)
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
	pgid, err := process.PGID(pid)
	if err != nil {
		slave.Close()
		_ = process.TerminateGroup(pid, process.DefaultGrace)
		_ = cfg.AgentCmd.Wait()
		return fmt.Errorf("failed to capture agent process group: %w", err)
	}

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
	// Restrict socket access to the owner only.
	_ = os.Chmod(cfg.SocketPath, 0600)
	defer func() {
		listener.Close()
		_ = os.Remove(cfg.SocketPath)
	}()

	if err := cfg.Manager.SetPTYSession(cfg.WorkspaceID, workspace.PTYSession{
		SocketPath:  cfg.SocketPath,
		DaemonPID:   daemonPID,
		DaemonPGID:  daemonPGID,
		DaemonStart: daemonStart,
	}); err != nil {
		_ = process.TerminateGroup(pgid, process.DefaultGrace)
		_ = cfg.AgentCmd.Wait()
		return fmt.Errorf("set PTY session: %w", err)
	}

	// 9. Initialize ring buffer
	ring := NewRingBuffer(cfg.RingSize)

	// 10. Set up daemon context
	daemonCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var clientWg sync.WaitGroup // tracks individual handleClient goroutines
	var clientMu sync.Mutex
	var currentClient net.Conn
	agentDone := make(chan struct{})

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
						configDir,
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
		defer close(agentDone)
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

		// Send exit message to current client, then close it so handleClient
		// unblocks immediately rather than waiting for the cancel deadline.
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
			_ = currentClient.Close()
		}
		clientMu.Unlock()

		// Clear session process from manager
		_ = cfg.Manager.ClearSessionProcess(cfg.WorkspaceID)
		_ = cfg.Manager.ClearPTYSession(cfg.WorkspaceID)

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

			// Track each client goroutine so shutdown can wait for them.
			clientWg.Add(1)
			go func(c net.Conn) {
				defer clientWg.Done()
				// When the daemon context is cancelled, unblock any blocking
				// read/write on this connection within 5 seconds.
				go func() {
					<-daemonCtx.Done()
					_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				}()
				handleClient(daemonCtx, c, &session, ring, &clientMu, &currentClient, master)
			}(conn)
		}
	}()

	// Wait for agent exit or external context cancellation. daemonCtx also
	// closes when the parent ctx is cancelled, so it cannot distinguish those
	// cases for shutdown decisions.
	select {
	case <-agentDone:
		// Agent exited
		fmt.Fprintf(os.Stderr, "[daemon] agent exited, shutting down\n")
	case <-ctx.Done():
		// External cancellation
		fmt.Fprintf(os.Stderr, "[daemon] context canceled, terminating agent\n")
		if process.IdentityMatches(pid, pgid, startedAt) {
			_ = process.TerminateGroup(pgid, process.DefaultGrace)
		} else {
			_ = cfg.AgentCmd.Process.Kill()
		}
	}

	// Close listener to stop accept loop
	listener.Close()

	// Wait for main goroutines (PTY reader, agent waiter, accept loop),
	// then for any in-flight client handlers.
	wg.Wait()
	clientWg.Wait()

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

	// Read MsgAttach from the client, which carries the initial terminal size.
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	attachMsg, err := Decode(scanner.Bytes())
	if err != nil || attachMsg.Type != MsgAttach {
		return
	}
	if attachMsg.Rows > 0 && attachMsg.Cols > 0 {
		_ = Setsize(master, attachMsg.Rows, attachMsg.Cols)
	}

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

	// Send MsgBuffer with ring buffer contents.
	bufferData := ring.Bytes()
	bufferMsg := Msg{
		Type: MsgBuffer,
		Data: EncodeData(bufferData),
	}
	encoded, err = Encode(bufferMsg)
	if err == nil {
		_, _ = conn.Write(encoded)
	}

	// Input/resize/detach loop
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
	configDir string,
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
			handleApprovalRequest(workspaceID, agent, command, configDir, timeout, master)
		}
	}

	// Also check the incomplete line for prompts (some prompts don't end with newline)
	if lastLine != "" {
		if detected, agent, command := DetectPrompt(lastLine); detected {
			handleApprovalRequest(workspaceID, agent, command, configDir, timeout, master)
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
// 4. Audit log the request
// 5. Wait for response (with timeout)
// 6. Inject approval/rejection to PTY
// 7. Audit log the outcome
// 8. Cleanup state
func handleApprovalRequest(
	workspaceID string,
	agent string,
	command string,
	configDir string,
	timeout time.Duration,
	master *os.File,
) {
	riskLevel := ClassifyRisk(command)
	req := state.NewApprovalRequest(workspaceID, agent, command, riskLevel, timeout)

	if err := state.WritePendingApproval(configDir, workspaceID, req); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to write pending approval: %v\n", err)
		injectResponse(master, false)
		return
	}

	if err := audit.LogRequest(configDir, workspaceID, agent, command, req.ID, riskLevel); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to audit log request: %v\n", err)
	}

	response := waitForApprovalResponse(configDir, workspaceID, timeout)

	if response == nil {
		fmt.Fprintf(os.Stderr, "[daemon] approval timeout for command: %s\n", command)
		injectResponse(master, false)
		if err := audit.LogTimeout(configDir, workspaceID, agent, command, req.ID, riskLevel); err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] failed to audit log timeout: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[daemon] approval response: approved=%v for command: %s\n", response.Approved, command)
		injectResponse(master, response.Approved)
		if response.Approved {
			if err := audit.LogApprove(configDir, workspaceID, agent, command, req.ID, response.RespondedBy, riskLevel); err != nil {
				fmt.Fprintf(os.Stderr, "[daemon] failed to audit log approval: %v\n", err)
			}
		} else {
			if err := audit.LogReject(configDir, workspaceID, agent, command, req.ID, response.RespondedBy, riskLevel); err != nil {
				fmt.Fprintf(os.Stderr, "[daemon] failed to audit log rejection: %v\n", err)
			}
		}
	}

	if err := state.CleanupApprovalState(configDir, workspaceID); err != nil {
		fmt.Fprintf(os.Stderr, "[daemon] failed to cleanup approval state: %v\n", err)
	}
}

// waitForApprovalResponse polls for an approval response with timeout.
// Returns nil on timeout, otherwise returns the response.
func waitForApprovalResponse(configDir, workspaceID string, timeout time.Duration) *state.ApprovalResponse {
	deadline := time.Now().Add(timeout)
	pollInterval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		response, err := state.ReadApprovalResponse(configDir, workspaceID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[daemon] error reading approval response: %v\n", err)
			time.Sleep(pollInterval)
			continue
		}

		if response != nil {
			return response
		}

		time.Sleep(pollInterval)
	}

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
