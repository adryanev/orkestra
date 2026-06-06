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
		if err := agentRunner.Stop(stopWorkspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Update workspace status to inactive
		ws, err := wm.GetWorkspace(stopWorkspace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not get workspace: %v\n", err)
		} else {
			ws.Status = "inactive"
			if err := wm.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save workspace status: %v\n", err)
			}
		}
		fmt.Printf("Workspace %s stopped\n", stopWorkspace)
	},
}

func init() {
	stopCmd.Flags().StringVar(&stopWorkspace, "workspace", "", "Workspace ID")
}