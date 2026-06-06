package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server (stdio)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(os.Stderr, "MCP server started, waiting for JSON-RPC on stdin...")
		mcpServer.Listen()
	},
}
