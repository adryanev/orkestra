package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/adryanev/orkestra/pkg/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var mcpWorkspace string

var mcpCmd = &cobra.Command{
	Use:   "mcp --workspace <workspace-id>",
	Short: "Start MCP server (stdio)",
	Run: func(cmd *cobra.Command, args []string) {
		if mcpWorkspace == "" {
			mcpWorkspace = os.Getenv("XORKESTRA_WORKSPACE")
		}
		if mcpWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}

		// Optional language-server overrides from config (lsp_servers key).
		var overrides []mcp.LspServerConfig
		_ = viper.UnmarshalKey("lsp_servers", &overrides)

		server, err := mcp.NewServer(workspaceManager, mcpWorkspace, overrides)
		if err != nil {
			emitError(err)
		}

		// Cancel on SIGINT/SIGTERM so the server and its LSP pool shut down
		// cleanly. Logging goes to stderr — stdout is the protocol stream.
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		fmt.Fprintln(os.Stderr, "orkestra MCP server started (stdio)")
		if err := server.Run(ctx); err != nil && ctx.Err() == nil {
			emitError(err)
		}
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpWorkspace, "workspace", "", "Workspace ID the server is bound to")
}
