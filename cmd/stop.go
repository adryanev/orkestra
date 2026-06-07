package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/adryanev/orkestra/pkg/process"
	"github.com/spf13/cobra"
)

var stopWorkspace string

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running agent process",
	Run: func(cmd *cobra.Command, args []string) {
		if stopWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}

		// Check if this is a PTY session that needs daemon cleanup.
		session, err := workspaceManager.GetSession(stopWorkspace)
		if err == nil && session.PTY != nil {
			// PTY session exists: validate daemon identity and terminate if alive.
			if process.IdentityMatches(session.PTY.DaemonPID, session.PTY.DaemonPGID, session.PTY.DaemonStart) {
				// Daemon is still alive: terminate its process group.
				if err := process.TerminateGroup(session.PTY.DaemonPGID, 5*time.Second); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to terminate PTY daemon for workspace %s: %v\n", stopWorkspace, err)
				}
			}
			// Remove socket (idempotent: ignore errors if already gone).
			if session.PTY.SocketPath != "" {
				_ = os.Remove(session.PTY.SocketPath)
			}
			// Clear PTY session state.
			if err := workspaceManager.ClearPTYSession(stopWorkspace); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to clear PTY session for workspace %s: %v\n", stopWorkspace, err)
			}
		}

		// The runner signals the agent's process group, clears process state,
		// and marks the workspace inactive (all under the cross-process lock).
		if err := agentRunner.Stop(stopWorkspace); err != nil {
			emitError(err)
		}
		emitResult(
			fmt.Sprintf("Workspace %s stopped", stopWorkspace),
			map[string]string{"workspace_id": stopWorkspace, "status": "stopped"},
		)
	},
}

func init() {
	stopCmd.Flags().StringVar(&stopWorkspace, "workspace", "", "Workspace ID")
}
