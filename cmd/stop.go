package cmd

import (
	"fmt"
	"os"

	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/workspace"
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
		if err := agentRunner.Stop(stopWorkspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Update workspace status to inactive
		ws, err := workspaceManager.GetWorkspace(stopWorkspace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not get workspace status for %s: %v\n", stopWorkspace, err)
		} else {
			ws.Status = "inactive"
			if err := workspaceManager.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to update workspace status for %s: %v\n", stopWorkspace, err)
			}
		}
		fmt.Printf("Workspace %s stopped\n", stopWorkspace)
	},
 }

func init() {
	stopCmd.Flags().StringVar(&stopWorkspace, "workspace", "", "Workspace ID")
}