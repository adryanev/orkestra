//go:build !windows

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/adryanev/orkestra/pkg/env"
	"github.com/adryanev/orkestra/pkg/gitauth"
	"github.com/adryanev/orkestra/pkg/pty"
	"github.com/adryanev/orkestra/pkg/runner"
	"github.com/spf13/cobra"
)

// unixSocketMaxLen reserves one byte for the sockaddr_un NUL terminator.
var unixSocketMaxLen = func() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}()

func ptySocketPathForWorkspace(workspaceID string) (string, error) {
	socketPath := filepath.Join(configDir, "pty", workspaceID+".sock")
	if len(socketPath) <= unixSocketMaxLen {
		return socketPath, nil
	}

	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("orkestra-pty-%d", os.Getuid()))
	socketPath = filepath.Join(tmpDir, workspaceID+".sock")
	if len(socketPath) > unixSocketMaxLen {
		return "", fmt.Errorf("socket path too long (>%d bytes): %s", unixSocketMaxLen, socketPath)
	}

	return socketPath, nil
}

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

		// Derive socket path: ~/.orkestra/pty/<ws-id>.sock, falling back to
		// a private temp subdirectory when the config path is too long.
		socketPath, err := ptySocketPathForWorkspace(ptyDaemonWorkspace)
		if err != nil {
			emitError(err)
		}

		// Ensure socket directory exists and is private before listening.
		socketDir := filepath.Dir(socketPath)
		if err := os.MkdirAll(socketDir, 0700); err != nil {
			emitError(fmt.Errorf("failed to create socket directory: %w", err))
		}
		if err := os.Chmod(socketDir, 0700); err != nil {
			emitError(fmt.Errorf("failed to restrict socket directory: %w", err))
		}

		// Run the PTY daemon
		cfg := pty.DaemonConfig{
			WorkspaceID: ptyDaemonWorkspace,
			SocketPath:  socketPath,
			AgentCmd:    agentCmd,
			RingSize:    64 * 1024, // 64KB ring buffer
			Manager:     workspaceManager,
			ExitCode:    new(int),
		}

		if err := pty.RunDaemon(context.Background(), cfg); err != nil {
			emitError(fmt.Errorf("PTY daemon failed: %w", err))
		}

		os.Exit(*cfg.ExitCode)
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
	binPath := runner.ResolveBinary(agent, shellEnv)

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
	cmdEnv := runner.ComposeEnv(shellEnv, token)

	// Create command
	cmd := exec.Command(binPath, args...)
	cmd.Dir = worktreePath
	cmd.Env = cmdEnv

	// Note: SysProcAttr for Setsid is set by pty.RunDaemon, not here

	return cmd, nil
}

func init() {
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonWorkspace, "workspace", "", "Workspace ID")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonAgent, "agent", "claude", "Agent type (claude or codex)")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonModel, "model", "", "Model to use")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonEffort, "effort", "", "Effort level for Claude Code")
	ptyDaemonCmd.Flags().StringVar(&ptyDaemonPrompt, "prompt", "", "Prompt text")

	rootCmd.AddCommand(ptyDaemonCmd)
}
