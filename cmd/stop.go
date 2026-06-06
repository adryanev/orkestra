package cmd

import "github.com/spf13/cobra"

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running agent process",
	Run: func(cmd *cobra.Command, args []string) {
		// Implementation for stop command
		cmd.Println("Stopping agent...")
	},
}