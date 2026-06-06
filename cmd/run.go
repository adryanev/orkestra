package cmd

import (
	"fmt"

	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/spf13/cobra"
)

var (
	runWorkspace string
	runPrompt    string
	runAgent     string
	runStream    bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run an agent in a workspace",
	Run: func(cmd *cobra.Command, args []string) {
		if runWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}
		if runPrompt == "" && len(args) == 0 {
			emitError(fmt.Errorf("prompt required (--prompt or argument)"))
		}
		prompt := runPrompt
		if prompt == "" {
			prompt = args[0]
		}

		// Git-auth token resolution and injection happens inside the runner,
		// so it applies to both run and resume.

		var agent runner.AgentType
		switch runAgent {
		case "claude":
			agent = runner.Claude
		case "codex":
			agent = runner.Codex
		default:
			emitError(fmt.Errorf("invalid --agent %q (expected claude or codex)", runAgent))
		}

		sessionInfo, err := agentRunner.Run(runWorkspace, agent, prompt, false, runStream, !jsonOutput)
		if err != nil {
			emitError(err)
		}
		// The runner persists the session id and clears process state on exit,
		// so the command only reports it.
		reportSession(runWorkspace, sessionInfo)
	},
}

// reportSession prints the captured session/thread id and usage from a run or
// resume, in human or JSON form. The JSON shape is stable across both commands.
func reportSession(workspaceID string, si *runner.SessionInfo) {
	if si == nil {
		return
	}
	if jsonOutput {
		emitResult("", map[string]interface{}{
			"workspace_id": workspaceID,
			"session_id":   si.SessionID,
			"thread_id":    si.ThreadID,
			"usage":        si.Usage,
		})
		return
	}
	if si.SessionID != "" {
		fmt.Printf("Session: %s\n", si.SessionID)
	}
	if si.ThreadID != "" {
		fmt.Printf("Thread: %s\n", si.ThreadID)
	}
	if si.Usage.Model != "" || si.Usage.InputTokens != 0 || si.Usage.OutputTokens != 0 {
		if si.Usage.Model != "" {
			fmt.Printf("Usage: model=%s input_tokens=%d output_tokens=%d\n",
				si.Usage.Model, si.Usage.InputTokens, si.Usage.OutputTokens)
		} else {
			fmt.Printf("Usage: input_tokens=%d output_tokens=%d\n",
				si.Usage.InputTokens, si.Usage.OutputTokens)
		}
	}
}

func init() {
	runCmd.Flags().StringVar(&runWorkspace, "workspace", "", "Workspace ID")
	runCmd.Flags().StringVar(&runPrompt, "prompt", "", "Prompt text")
	runCmd.Flags().StringVar(&runAgent, "agent", "claude", "Agent type (claude or codex)")
	runCmd.Flags().BoolVar(&runStream, "stream", false, "Output raw NDJSON/JSONL instead of parsed text")
}
