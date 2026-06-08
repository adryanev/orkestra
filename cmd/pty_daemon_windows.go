//go:build windows

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ptyDaemonCmd is a no-op stub on Windows since PTY operations are not supported.
var ptyDaemonCmd = &cobra.Command{
	Use:    "__pty-daemon",
	Short:  "Internal: PTY daemon process (not supported on Windows)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		emitError(fmt.Errorf("PTY daemon is not supported on Windows"))
	},
}

func init() {
	// Register flags for consistency, even though they won't be used
	ptyDaemonCmd.Flags().String("workspace", "", "Workspace ID")
	ptyDaemonCmd.Flags().String("agent", "claude", "Agent type (claude or codex)")
	ptyDaemonCmd.Flags().String("model", "", "Model to use")
	ptyDaemonCmd.Flags().String("effort", "", "Effort level for Claude Code")
	ptyDaemonCmd.Flags().String("prompt", "", "Prompt text")

	rootCmd.AddCommand(ptyDaemonCmd)
}
