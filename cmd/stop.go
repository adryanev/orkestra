package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)
var stopWorkspace string

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running agent process",
	Run: func(cmd *cobra.Command, args []string) {
		if stopWorkspace == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --workspace is required")
			os.Exit(1)
		}
		// The runner signals the agent's process group, clears process state,
		// and marks the workspace inactive (all under the cross-process lock).
		if err := agentRunner.Stop(stopWorkspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Workspace %s stopped\n", stopWorkspace)
	},
}

func init() {
	stopCmd.Flags().StringVar(&stopWorkspace, "workspace", "", "Workspace ID")
}