package cmd

import (
	"fmt"

	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	workspaceRepo      string
	workspaceName      string
	workspaceBranch    string
	workspaceGhProfile string
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage workspaces",
}

var workspaceCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new workspace",
	Run: func(cmd *cobra.Command, args []string) {
		if workspaceRepo == "" {
			emitError(fmt.Errorf("--repo is required"))
		}
		ws, err := wm.CreateWorkspace(workspaceName, workspaceRepo, workspaceBranch, workspaceGhProfile)
		if err != nil {
			emitError(err)
		}
		human := fmt.Sprintf("Workspace created: %s (%s) at %s", ws.Name, ws.ID, ws.WorktreePath)
		if ws.GhProfile != "" {
			human += fmt.Sprintf("\n  GH Profile: %s", ws.GhProfile)
		}
		emitResult(human, ws)
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	Run: func(cmd *cobra.Command, args []string) {
		workspaces, err := wm.ListWorkspaces()
		if err != nil {
			emitError(err)
		}
		if jsonOutput {
			// Always emit an array (never null) for a stable shape.
			if workspaces == nil {
				workspaces = []workspace.Workspace{}
			}
			emitResult("", workspaces)
			return
		}
		if len(workspaces) == 0 {
			fmt.Println("No workspaces found")
			return
		}
		for _, ws := range workspaces {
			profile := ws.GhProfile
			if profile == "" {
				profile = "-"
			}
			fmt.Printf("  %s | %s | %s | %s | %s\n", ws.ID[:8], ws.Name, ws.Branch, ws.Status, profile)
		}
	},
}

func init() {
	workspaceCreateCmd.Flags().StringVar(&workspaceRepo, "repo", "", "Repository path")
	workspaceCreateCmd.Flags().StringVar(&workspaceName, "name", "", "Workspace name")
	workspaceCreateCmd.Flags().StringVar(&workspaceBranch, "branch", "", "Branch name (optional)")
	workspaceCreateCmd.Flags().StringVar(&workspaceGhProfile, "gh-profile", "", "GitHub auth profile")

	workspaceCmd.AddCommand(workspaceCreateCmd)
	workspaceCmd.AddCommand(workspaceListCmd)
}
