package cmd

import (
	"fmt"
	"os"

	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/spf13/cobra"
)

var (
	resumeWorkspace string
	resumePrompt    string
	resumeAgent     string
)

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume a previous agent session",
	Run: func(cmd *cobra.Command, args []string) {
		// Retrieve session info for resuming
		session, err := workspaceManager.GetSession(resumeWorkspace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not get session info for workspace %s: %v\n", resumeWorkspace, err)
			os.Exit(1)
		}

		// Ensure the agent type matches
		if session.Agent != resumeAgent {
			fmt.Fprintf(os.Stderr, "Error: agent type mismatch. Workspace %s uses %s, but requested %s\n", resumeWorkspace, session.Agent, resumeAgent)
			os.Exit(1)
		}

		// TODO: Pass saved SessionID and ThreadID to agentRunner.Run if the runner supports it
		// For now, we re-initialize the session by calling Run with the prompt.
		sessionInfo, err := agentRunner.Run(resumeWorkspace, agent, prompt, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if sessionInfo != nil && sessionInfo.SessionID != "" {
			fmt.Printf("Resumed session: %s\n", sessionInfo.SessionID)
		}
	},
};

func init() {
	resumeCmd.Flags().StringVar(&resumeWorkspace, "workspace", "", "Workspace ID")
	resumeCmd.Flags().StringVar(&resumePrompt, "prompt", "", "Prompt text")
	resumeCmd.Flags().StringVar(&resumeAgent, "agent", "claude", "Agent type (claude or codex)")
}