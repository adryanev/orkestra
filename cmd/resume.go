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
		if resumeWorkspace == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: --workspace is required")
			os.Exit(1)
		}
		if resumePrompt == "" && len(args) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Error: prompt required")
			os.Exit(1)
		}
		prompt := resumePrompt
		if prompt == "" {
			prompt = args[0]
		}

		a := runner.Claude
		if resumeAgent == "codex" {
			a = runner.Codex
		}

		sessionInfo, err := agentRunner.Run(resumeWorkspace, a, prompt, true, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if sessionInfo != nil && sessionInfo.SessionID != "" {
			fmt.Printf("Resumed session: %s\n", sessionInfo.SessionID)
		}
	},
}

func init() {
	resumeCmd.Flags().StringVar(&resumeWorkspace, "workspace", "", "Workspace ID")
	resumeCmd.Flags().StringVar(&resumePrompt, "prompt", "", "Prompt text")
	resumeCmd.Flags().StringVar(&resumeAgent, "agent", "claude", "Agent type (claude or codex)")
}