//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/runner"
)

// runPTYMode spawns the PTY daemon, polls for socket readiness, and exits with
// attach instructions. It checks for duplicate PTY sessions first.
func runPTYMode(workspaceID string, agent runner.AgentType, prompt, model, effort string) {
	// Check for duplicate PTY session.
	session, err := workspaceManager.GetSession(workspaceID)
	if err == nil && session.PTY != nil {
		// A PTY record exists. Verify if the daemon is still alive.
		if process.IdentityMatches(session.PTY.DaemonPID, session.PTY.DaemonPGID, session.PTY.DaemonStart) {
			emitError(fmt.Errorf("PTY session already running for workspace %s; use 'orkestra attach --workspace %s'", workspaceID, workspaceID))
		}
		// Stale PTY record: clear it and proceed.
		if clearErr := workspaceManager.ClearPTYSession(workspaceID); clearErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clear stale PTY session for %s: %v\n", workspaceID, clearErr)
		}
	}

	// Derive socket path (same logic as daemon).
	socketPath, err := ptySocketPathForWorkspace(workspaceID)
	if err != nil {
		emitError(err)
	}

	// Build daemon invocation.
	orkestraBin, err := os.Executable()
	if err != nil {
		emitError(fmt.Errorf("failed to resolve orkestra binary path: %w", err))
	}

	daemonArgs := []string{"__pty-daemon", "--workspace", workspaceID, "--agent", string(agent), "--prompt", prompt}
	if model != "" {
		daemonArgs = append(daemonArgs, "--model", model)
	}
	if effort != "" {
		daemonArgs = append(daemonArgs, "--effort", effort)
	}

	daemonCmd := exec.Command(orkestraBin, daemonArgs...)
	// Start daemon in its own session so it survives parent exit.
	daemonCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Detach stdio: daemon writes to socket, not stdout/stderr.
	daemonCmd.Stdin = nil
	daemonCmd.Stdout = nil
	daemonCmd.Stderr = nil

	if err := daemonCmd.Start(); err != nil {
		emitError(fmt.Errorf("failed to start PTY daemon: %w", err))
	}

	// Parent doesn't wait for daemon; it's fully detached.
	// Poll socket for readiness: 100ms intervals, 5s timeout.
	timeout := 5 * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			// Socket exists: daemon is ready.
			if jsonOutput {
				emitResult("", map[string]interface{}{
					"workspace_id": workspaceID,
					"socket_path":  socketPath,
					"status":       "ready",
				})
			} else {
				fmt.Printf("PTY daemon started for workspace %s\n", workspaceID)
				fmt.Printf("Socket: %s\n", socketPath)
				fmt.Printf("\nAttach to the session with:\n")
				fmt.Printf("  orkestra attach --workspace %s\n", workspaceID)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Timeout: daemon failed to start.
	emitError(fmt.Errorf("PTY daemon failed to start (socket not created within %v)", timeout))
}
