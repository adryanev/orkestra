package cmd

import "github.com/spf13/cobra"

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run an agent in a workspace",
	Run: func(cmd *cobra.Command, args []string) {
		// Implementation for run command
		cmd.Println("Running agent...")
	},
}