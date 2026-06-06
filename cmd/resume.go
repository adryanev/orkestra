package cmd

import (
	"fmt"

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
			emitError(fmt.Errorf("--workspace is required"))
		}
		if resumePrompt == "" && len(args) == 0 {
			emitError(fmt.Errorf("prompt required (--prompt or argument)"))
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
			emitError(err)
		}
		reportSession(resumeWorkspace, sessionInfo)
	},
}

func init() {
	resumeCmd.Flags().StringVar(&resumeWorkspace, "workspace", "", "Workspace ID")
	resumeCmd.Flags().StringVar(&resumePrompt, "prompt", "", "Prompt text")
	resumeCmd.Flags().StringVar(&resumeAgent, "agent", "claude", "Agent type (claude or codex)")
}
