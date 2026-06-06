package cmd

import "github.com/spf13/cobra"

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server commands",
	Run: func(cmd *cobra.Command, args []string) {
		// Implementation for mcp command
		cmd.Println("MCP server commands...")
	},
}