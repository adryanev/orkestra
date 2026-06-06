package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	workspaceRepo     string
	workspaceName     string
	workspaceBranch   string
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
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --repo is required")
			return
		}
		ws, err := wm.CreateWorkspace(workspaceName, workspaceRepo, workspaceBranch, workspaceGhProfile)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
			return
		}
		fmt.Printf("Workspace created: %s (%s) at %s\n", ws.Name, ws.ID, ws.WorktreePath)
		if ws.GhProfile != "" {
			fmt.Printf("  GH Profile: %s\n", ws.GhProfile)
		}
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	Run: func(cmd *cobra.Command, args []string) {
		workspaces, err := wm.ListWorkspaces()
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Error: %v\n", err)
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