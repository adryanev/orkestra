package cmd

import (
	"fmt"
	"time"

	"github.com/adryanev/orkestra/pkg/mcp"
	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/spf13/cobra"
)

var (
	resumeWorkspace string
	resumePrompt    string
	resumeAgent     string
	resumeModel     string
	resumeEffort    string
	resumeAnswer    string
)

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume a previous agent session",
	Run: func(cmd *cobra.Command, args []string) {
		if resumeWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}

		// Handle --answer flag (R3)
		var prompt string
		if resumeAnswer != "" {
			configDir := getConfigDir()
			pending, err := mcp.ReadPending(configDir, resumeWorkspace)
			if err != nil {
				emitError(fmt.Errorf("failed to read pending question: %w", err))
			}
			if pending == nil {
				emitError(fmt.Errorf("no pending question for workspace %s", resumeWorkspace))
			}

			// Synthesize continuation prompt with Q&A
			prompt = fmt.Sprintf(`Your ask_user tool call was answered.

Question: %s
Answer: %s

Resume the task from where you left off.`, pending.Question, resumeAnswer)

			// Append user-provided prompt if present
			if resumePrompt != "" {
				prompt = prompt + "\n\n" + resumePrompt
			}

			// Delete pending file
			if err := mcp.DeletePending(configDir, resumeWorkspace); err != nil {
				emitError(fmt.Errorf("failed to delete pending file: %w", err))
			}

			// Write answer audit record
			record := mcp.AnswerRecord{
				WorkspaceID: resumeWorkspace,
				Question:    pending.Question,
				Options:     pending.Options,
				Answer:      resumeAnswer,
				AskedAt:     pending.AskedAt,
				AnsweredAt:  time.Now().UTC(),
			}
			if err := mcp.AppendAnswer(configDir, resumeWorkspace, record); err != nil {
				emitError(fmt.Errorf("failed to write answer record: %w", err))
			}
		} else {
			// Normal resume (no --answer)
			if resumePrompt == "" && len(args) == 0 {
				emitError(fmt.Errorf("prompt required (--prompt or argument)"))
			}
			prompt = resumePrompt
			if prompt == "" {
				prompt = args[0]
			}
		}

		var a runner.AgentType
		switch resumeAgent {
		case "claude":
			a = runner.Claude
		case "codex":
			a = runner.Codex
		default:
			emitError(fmt.Errorf("invalid --agent %q (expected claude or codex)", resumeAgent))
		}

		sessionInfo, err := agentRunner.Run(resumeWorkspace, a, prompt, true, false, !jsonOutput, resumeModel, resumeEffort)
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
	resumeCmd.Flags().StringVar(&resumeModel, "model", "", "Model to use (overrides saved model)")
	resumeCmd.Flags().StringVar(&resumeEffort, "effort", "", "Effort level for Claude Code (low, medium, high, xhigh, max)")
	resumeCmd.Flags().StringVar(&resumeAnswer, "answer", "", "Answer to pending question (reads and deletes pending/<ws-id>.json)")
}
