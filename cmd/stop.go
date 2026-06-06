package cmd

import (
	"fmt"

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
