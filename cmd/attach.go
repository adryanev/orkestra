package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/adryanev/orkestra/pkg/pty"
	"github.com/spf13/cobra"
)

var attachWorkspace string

var attachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Attach to a persistent PTY session",
	Long: `Attach to a persistent PTY session for a workspace.

The attach command connects your terminal to a running PTY daemon session,
allowing you to interact with the agent in real-time. You can detach from
the session at any time using the detach sequence (Ctrl+\ Ctrl+_) without
stopping the agent.

The agent continues running in the background after you detach, and you can
reattach later to resume interaction.`,
	Run: func(cmd *cobra.Command, args []string) {
		if attachWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}

		// Get the session to find the socket path
		session, err := workspaceManager.GetSession(attachWorkspace)
		if err != nil {
			emitError(fmt.Errorf("failed to get session for workspace %s: %w", attachWorkspace, err))
		}

		if session.PTY == nil {
			emitError(fmt.Errorf("workspace %s has no active PTY session", attachWorkspace))
		}

		socketPath := session.PTY.SocketPath
		if socketPath == "" {
			emitError(fmt.Errorf("workspace %s PTY session has no socket path", attachWorkspace))
		}

		// Check if socket exists
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			emitError(fmt.Errorf("PTY socket does not exist: %s (daemon may have exited)", socketPath))
		}

		// Attach to the PTY session
		ctx := context.Background()
		if err := pty.RunAttach(ctx, socketPath, int(os.Stdin.Fd()), os.Stdout); err != nil {
			emitError(fmt.Errorf("attach failed: %w", err))
		}

		// Successfully detached or session ended
		if !jsonOutput {
			fmt.Fprintln(os.Stderr, "\n[Detached from PTY session]")
		}
	},
}

func init() {
	attachCmd.Flags().StringVar(&attachWorkspace, "workspace", "", "Workspace ID to attach to")
	attachCmd.MarkFlagRequired("workspace")
}
