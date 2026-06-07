//go:build !windows

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adryanev/orkestra/pkg/env"
	"github.com/adryanev/orkestra/pkg/gitauth"
	"github.com/adryanev/orkestra/pkg/pty"
	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/spf13/cobra"
)

// unixSocketMaxLen is the platform limit for Unix socket paths.
const unixSocketMaxLen = 108

var (
	ptyDaemonWorkspace string
	ptyDaemonAgent     string
	ptyDaemonModel     string
	ptyDaemonEffort    string
	ptyDaemonPrompt    string
)

// ptyDaemonCmd is a hidden internal command that runs the PTY broker daemon.
// It is invoked by `orkestra attach` when spawning a background daemon process.
var ptyDaemonCmd = &cobra.Command{
	Use:    "__pty-daemon",
	Short:  "Internal: PTY daemon process (do not invoke directly)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		if ptyDaemonWorkspace == "" {
			emitError(fmt.Errorf("--workspace is required"))
		}
		if ptyDaemonAgent == "" {
			emitError(fmt.Errorf("--agent is required"))
		}
		if ptyDaemonPrompt == "" && len(args) == 0 {
			emitError(fmt.Errorf("prompt required (--prompt or argument)"))
		}

		prompt := ptyDaemonPrompt
		if prompt == "" {
			prompt = args[0]
		}

		// Validate agent type
		var agent runner.AgentType
		switch ptyDaemonAgent {
		case "claude":
			agent = runner.Claude
		case "codex":
			agent = runner.Codex
		default:
			emitError(fmt.Errorf("invalid --agent %q (expected claude or codex)", ptyDaemonAgent))
		}

		// Resolve workspace via Manager
		ws, err := workspaceManager.GetWorkspace(ptyDaemonWorkspace)
		if err != nil {
			emitError(fmt.Errorf("failed to get workspace %s: %w", ptyDaemonWorkspace, err))
		}

		worktreePath := ws.WorktreePath
		ghProfile := ws.GhProfile

		// Resolve GitHub token via gitauth.ResolveToken
		var token string
		if ghProfile != "" {
			t, err := gitauth.ResolveToken(ghProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to resolve gh token for profile %s: %v\n", ghProfile, err)
			} else {
				token = t
			}
		}

		// Capture shell environment
		shellEnv := env.Captured()

		// Build agent command (don't start yet)
		agentCmd, err := buildAgentCommand(agent, prompt, worktreePath, shellEnv, token, ptyDaemonModel, ptyDaemonEffort)
		if err != nil {
			emitError(fmt.Errorf("failed to build agent command: %w", err))
		}

		// Derive socket path: ~/.orkestra/pty/<ws-id>.sock
		socketPath := filepath.Join(configDir, "pty", ptyDaemonWorkspace+".sock")

		// Check Unix socket path length limit (≤ 108 bytes on most systems)
		if len(socketPath) > unixSocketMaxLen {
			// Fallback: $TMPDIR/orkestra-<id>.sock
			tmpDir := os.TempDir()
			socketPath = filepath.Join(tmpDir, "orkestra-"+ptyDaemonWorkspace+".sock")

			if len(socketPath) > unixSocketMaxLen {
				emitError(fmt.Errorf("socket path too long (>%d bytes): %s", unixSocketMaxLen, socketPath))
			}
		}

		// Ensure socket directory exists
		socketDir := filepath.Dir(socketPath)
		if err := os.MkdirAll(socketDir, 0755); err != nil {
			emitError(fmt.Errorf("failed to create socket directory: %w", err))
		}

		// Run the PTY daemon
		cfg := pty.DaemonConfig{
			WorkspaceID: ptyDaemonWorkspace,
			SocketPath:  socketPath,
			AgentCmd:    agentCmd,
			RingSize:    64 * 1024, // 64KB ring buffer
			Manager:     workspaceManager,
		}

		if err := pty.RunDaemon(context.Background(), cfg); err != nil {
			emitError(fmt.Errorf("PTY daemon failed: %w", err))
		}

		// Exit with agent's exit code (handled by RunDaemon's Wait)
		os.Exit(0)
	},
}

// buildAgentCommand constructs the agent exec.Cmd with the appropriate
// arguments and environment, but does not start it yet.
func buildAgentCommand(
	agent runner.AgentType,
	prompt string,
	worktreePath string,
	shellEnv *env.ShellEnv,
	token string,
	model string,
	effort string,
) (*exec.Cmd, error) {
	// Resolve binary path
	binPath := resolveBinary(agent, shellEnv)

	// Build agent arguments
	var args []string
	switch agent {
	case runner.Claude:
		args = []string{"--output-format", "stream-json", "--verbose", "-p", prompt}
		args = append(args, "--permission-mode", "bypassPermissions")

		// Add model selection if specified
		if model != "" {
			args = append(args, "--model", model)
		}

		// Add effort level if specified (Claude Code only)
		if effort != "" {
			args = append(args, "--effort", effort)
		}

		// Disable agent's built-in LSP tool
		args = append(args, "--disallowedTools", "EnterWorktree,ExitWorktree,LSP")

		// Note: MCP config wiring is omitted for now in the daemon path,
		// as the daemon is already detached and may not need MCP callbacks

	case runner.Codex:
		args = []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox"}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, prompt)

	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agent)
	}

	// Build environment
	cmdEnv := composeEnv(shellEnv, token)

	// Create command
	cmd := exec.Command(binPath, args...)
	cmd.Dir = worktreePath
	cmd.Env = cmdEnv

	// Note: SysProcAttr for Setsid is set by pty.RunDaemon, not here

	return cmd, nil
}

// resolveBinary returns the absolute path to the agent binary from the captured
// shell environment, falling back to the bare command name.
func resolveBinary(agent runner.AgentType, shell *env.ShellEnv) string {
	if shell != nil {
		switch agent {
		case runner.Claude:
			if shell.ClaudePath != "" {
				return shell.ClaudePath
			}
		case runner.Codex:
			if shell.CodexPath != "" {
				return shell.CodexPath
			}
		}
	}
	if agent == runner.Codex {
		return "codex"
	}
	return "claude"
}

// composeEnv builds the child environment: the orkestra process environment,
// overlaid with captured login-shell variables, overlaid with GH_TOKEN. Keys
// are deduplicated (last writer wins) so the child sees one value per key.
func composeEnv(shell *env.ShellEnv, token string) []string {
	merged := make(map[string]string)
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				merged[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if shell != nil {
		for k, v := range shell.AllVars {
			merged[k] = v
		}
	}
	if token != "" {
		merged["GH_TOKEN"] = token
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func init() {
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonWorkspace, "workspace", "", "Workspace ID")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonAgent, "agent", "claude", "Agent type (claude or codex)")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonModel, "model", "", "Model to use")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonEffort, "effort", "", "Effort level for Claude Code")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonPrompt, "prompt", "", "Prompt text")

	rootCmd.AddCommand(ptyDaemonCmd)
}
