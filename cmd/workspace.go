package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	workspaceRepo        string
	workspaceName        string
	workspaceBranch      string
	workspaceGhProfile   string
	workspaceBaseBranch  string
	workspaceRemoveID    string
	workspaceRemoveForce bool
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
		ws, err := wm.CreateWorkspace(workspaceName, workspaceRepo, workspaceBranch, workspaceGhProfile, workspaceBaseBranch)
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

var workspaceRemoveCmd = &cobra.Command{
	Use:     "remove",
	Aliases: []string{"delete"},
	Short:   "Remove a workspace and its worktree",
	Run: func(cmd *cobra.Command, args []string) {
		if workspaceRemoveID == "" {
			emitError(fmt.Errorf("--id is required"))
		}

		if !workspaceRemoveForce {
			fmt.Fprintf(cmd.OutOrStdout(), "Are you sure you want to remove workspace %s and its worktree? [y/N] ", workspaceRemoveID)
			scanner := bufio.NewScanner(cmd.InOrStdin())
			scanner.Scan()
			if answer := strings.TrimSpace(scanner.Text()); answer != "y" && answer != "Y" {
				fmt.Fprintln(cmd.OutOrStdout(), "Cancelled")
				return
			}
		}

		// Guard against tearing down a workspace with a live agent. With
		// --force, stop the agent first; otherwise refuse.
		if agentRunner.IsRunning(workspaceRemoveID) {
			if !workspaceRemoveForce {
				emitError(fmt.Errorf("workspace %s has a running agent; pass --force to stop and remove it", workspaceRemoveID))
			}
			if err := agentRunner.Stop(workspaceRemoveID); err != nil {
				emitError(fmt.Errorf("failed to stop running agent: %w", err))
			}
		}

		if err := wm.RemoveWorkspace(workspaceRemoveID, workspaceRemoveForce); err != nil {
			emitError(err)
		}
		emitResult(
			fmt.Sprintf("Workspace %s removed", workspaceRemoveID),
			map[string]string{"workspace_id": workspaceRemoveID, "status": "removed"},
		)
	},
}

func init() {
	workspaceCreateCmd.Flags().StringVar(&workspaceRepo, "repo", "", "Repository path")
	workspaceCreateCmd.Flags().StringVar(&workspaceName, "name", "", "Workspace name")
	workspaceCreateCmd.Flags().StringVar(&workspaceBranch, "branch", "", "Branch name (optional)")
	workspaceCreateCmd.Flags().StringVar(&workspaceGhProfile, "gh-profile", "", "GitHub auth profile")
	workspaceCreateCmd.Flags().StringVar(&workspaceBaseBranch, "base-branch", "", "Base branch to create worktree from (bare name, e.g. 'main','develop'; tool prepends 'origin/')")

	workspaceRemoveCmd.Flags().StringVar(&workspaceRemoveID, "id", "", "Workspace ID")
	workspaceRemoveCmd.Flags().BoolVar(&workspaceRemoveForce, "force", false, "Force removal of a dirty worktree or a workspace with a running agent")

	workspaceCmd.AddCommand(workspaceCreateCmd)
	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceRemoveCmd)
}
