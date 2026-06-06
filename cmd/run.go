package cmd

import (
	"fmt"
	"os"

	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	runWorkspace string
	runPrompt    string
	runAgent     string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run an agent in a workspace",
	Run: func(cmd *cobra.Command, args []string) {
		if runWorkspace == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --workspace is required")
			os.Exit(1)
		}
		if runPrompt == "" && len(args) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: prompt required (--prompt or argument)")
			os.Exit(1)
		}
		prompt := runPrompt
		if prompt == "" {
			prompt = args[0]
		}

		agent := runner.Claude
		if runAgent == "codex" {
			agent = runner.Codex
		}

		sessionInfo, err := agentRunner.Run(runWorkspace, agent, prompt, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if sessionInfo != nil {
			if sessionInfo.SessionID != "" {
				fmt.Printf("Session: %s\n", sessionInfo.SessionID)
			}
			if sessionInfo.ThreadID != "" {
				fmt.Printf("Thread: %s\n", sessionInfo.ThreadID)
			}
			// Save session
			s := workspace.Session{
				WorkspaceID: runWorkspace,
				Agent:       "claude",
				SessionID:   sessionInfo.SessionID,
				ThreadID:    sessionInfo.ThreadID,
			}
			if runAgent == "codex" {
				s.Agent = "codex"
			}
			if err := wm.AddSession(s); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save session: %v\n", err)
			}
		}
	},
}

func init() {
	runCmd.Flags().StringVar(&runWorkspace, "workspace", "", "Workspace ID")
	runCmd.Flags().StringVar(&runPrompt, "prompt", "", "Prompt text")
	runCmd.Flags().StringVar(&runAgent, "agent", "claude", "Agent type (claude or codex)")
}