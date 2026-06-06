package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/adryanev/orkestra/pkg/env"
	"github.com/adryanev/orkestra/pkg/gitauth"
	"github.com/adryanev/orkestra/pkg/mcp"
	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/workspace"
)

// codexMCPTimeout bounds the codex mcp add/remove calls.
const codexMCPTimeout = 15 * time.Second

// codexRegisterMCP registers the orkestra MCP server with codex under name,
// bound to workspaceID. It removes any prior entry first so the operation is
// idempotent.
func codexRegisterMCP(name, orkestraBin, workspaceID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexMCPTimeout)
	defer cancel()
	// Best-effort removal of a stale entry; ignore its error.
	_ = exec.CommandContext(ctx, "codex", "mcp", "remove", name).Run()
	add := exec.CommandContext(ctx, "codex", "mcp", "add", name, "--", orkestraBin, "mcp", "--workspace", workspaceID)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// codexUnregisterMCP removes the orkestra MCP server registration after a run.
func codexUnregisterMCP(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), codexMCPTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "codex", "mcp", "remove", name).Run()
}

// AgentType defines the type of agent being run.
type AgentType string

const (
	Claude AgentType = "claude"
	Codex  AgentType = "codex"
)

type SessionInfo struct {
	SessionID string      `json:"session_id,omitempty"`
	ThreadID  string      `json:"thread_id,omitempty"`
	Usage     UsageReport `json:"usage,omitempty"`
}

type UsageReport struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Model        string `json:"model"`
}

type Runner struct {
	workspaceManager *workspace.Manager
	// binOverride, when set, replaces agent-binary resolution. It is a test
	// seam so a fake agent script can be driven through the full run path.
	binOverride string
	sync.Mutex
}

func NewRunner(wm *workspace.Manager) *Runner {
	return &Runner{workspaceManager: wm}
}

// Run executes an agent command in a given workspace. The runner lock guards
// only the short setup section; it is released before the agent executes so a
// concurrent Stop is never blocked by a long-running agent (KTD4).
func (r *Runner) Run(workspaceID string, agent AgentType, prompt string, resume bool, stream bool) (*SessionInfo, error) {
	r.Lock()
	ws, err := r.workspaceManager.GetWorkspace(workspaceID)
	if err != nil {
		r.Unlock()
		return nil, fmt.Errorf("failed to get workspace %s: %w", workspaceID, err)
	}
	worktreePath := ws.WorktreePath
	ghProfile := ws.GhProfile

	// On resume, look up the saved session/thread id so it can be passed to
	// the agent. Without it, `--resume` has nothing to continue.
	var resumeSessionID, resumeThreadID string
	if resume {
		s, err := r.workspaceManager.GetSession(workspaceID)
		if err != nil {
			r.Unlock()
			return nil, fmt.Errorf("cannot resume workspace %s: no saved session (run it first): %w", workspaceID, err)
		}
		resumeSessionID = s.SessionID
		resumeThreadID = s.ThreadID
	}

	// Resolve the GitHub token for the workspace profile so it can be injected
	// into the agent environment (works for both run and resume).
	var token string
	if ghProfile != "" {
		t, err := gitauth.ResolveToken(ghProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to resolve gh token for profile %s: %v\n", ghProfile, err)
		} else {
			token = t
		}
	}
	r.Unlock()

	sessionInfo, err := r.executeAgent(workspaceID, worktreePath, agent, prompt, resumeSessionID, resumeThreadID, token, stream)
	if err != nil {
		return sessionInfo, fmt.Errorf("agent execution failed for workspace %s: %w", workspaceID, err)
	}

	// Update workspace status
	if err := r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "active"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update workspace status for %s: %v\n", workspaceID, err)
	}

	return sessionInfo, nil
}

// IsRunning reports whether the workspace has a live agent process, based on
// the persisted PID. A stale or absent record reads as not running.
func (r *Runner) IsRunning(workspaceID string) bool {
	s, err := r.workspaceManager.GetSession(workspaceID)
	if err != nil {
		return false
	}
	return s.PID > 0 && process.Alive(s.PID)
}

// Stop terminates the agent process group recorded for a workspace. It reads
// the persisted PID/PGID (written by a separate `run` process), signals the
// group, then clears the process record and marks the workspace inactive. A
// workspace with no recorded process is treated as already stopped.
func (r *Runner) Stop(workspaceID string) error {
	s, err := r.workspaceManager.GetSession(workspaceID)
	if err != nil {
		// No session record at all: nothing to stop, idempotent success.
		_ = r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "inactive")
		return nil
	}

	if s.PGID > 0 {
		if err := process.TerminateGroup(s.PGID, process.DefaultGrace); err != nil {
			return fmt.Errorf("failed to terminate agent for workspace %s: %w", workspaceID, err)
		}
	}

	if err := r.workspaceManager.ClearSessionProcess(workspaceID); err != nil {
		return fmt.Errorf("failed to clear process state for workspace %s: %w", workspaceID, err)
	}
	if err := r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "inactive"); err != nil {
		return fmt.Errorf("failed to update workspace status for %s: %w", workspaceID, err)
	}
	return nil
}

// resolveBinary returns the absolute path to the agent binary from the captured
// shell environment, falling back to the bare command name.
func resolveBinary(agent AgentType, shell *env.ShellEnv) string {
	if shell != nil {
		switch agent {
		case Claude:
			if shell.ClaudePath != "" {
				return shell.ClaudePath
			}
		case Codex:
			if shell.CodexPath != "" {
				return shell.CodexPath
			}
		}
	}
	if agent == Codex {
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
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[kv[:i]] = kv[i+1:]
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

// buildAgentArgs constructs the binary name and argument vector for an agent
// invocation. Pulled out of executeAgent so the resume/session and MCP wiring
// is unit testable without spawning a process. mcpConfigPath, when non-empty,
// wires Claude's --mcp-config so the agent can call back into the orkestra MCP
// server.
func buildAgentArgs(agent AgentType, prompt, resumeSessionID, resumeThreadID, mcpConfigPath string) (string, []string, error) {
	switch agent {
	case Claude:
		args := []string{"--output-format", "stream-json", "--verbose", "-p", prompt}
		// Continue the prior session when a session id is known.
		if resumeSessionID != "" {
			args = append(args, "--resume", resumeSessionID)
		}
		args = append(args, "--permission-mode", "bypassPermissions")
		// Disable the agent's built-in LSP tool so the orkestra LSP tools are
		// used instead (orkestra manages LSP centrally, as Korlap does).
		args = append(args, "--disallowedTools", "EnterWorktree,ExitWorktree,LSP")
		if mcpConfigPath != "" {
			args = append(args, "--mcp-config", mcpConfigPath)
		}
		return "claude", args, nil

	case Codex:
		if resumeThreadID != "" {
			// codex exec resume <thread_id> --json "prompt"
			return "codex", []string{"exec", "resume", resumeThreadID, "--json", prompt}, nil
		}
		return "codex", []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox", prompt}, nil

	default:
		return "", nil, fmt.Errorf("unsupported agent type: %s", agent)
	}
}

func (r *Runner) executeAgent(workspaceID, worktreePath string, agent AgentType, prompt, resumeSessionID, resumeThreadID, token string, stream bool) (*SessionInfo, error) {
	// Wire the orkestra MCP server into the agent so it can call back. Claude
	// receives an --mcp-config file; Codex is registered before the run and
	// unregistered after exit.
	orkestraBin, _ := os.Executable()
	var mcpConfigPath string
	if agent == Claude && orkestraBin != "" {
		if p, err := mcp.WriteClaudeConfig(r.workspaceManager.ConfigDir(), workspaceID, orkestraBin); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to write MCP config for %s: %v\n", workspaceID, err)
		} else {
			mcpConfigPath = p
		}
	}
	if agent == Codex && orkestraBin != "" {
		name := mcp.CodexServerName(workspaceID)
		if err := codexRegisterMCP(name, orkestraBin, workspaceID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to register Codex MCP server: %v\n", err)
		} else {
			defer codexUnregisterMCP(name)
		}
	}

	_, args, err := buildAgentArgs(agent, prompt, resumeSessionID, resumeThreadID, mcpConfigPath)
	if err != nil {
		return nil, err
	}

	shellEnv := env.Captured()
	// Resolve the agent binary against the captured shell PATH. exec.Command
	// resolves bare names against orkestra's own PATH, not cmd.Env, so an
	// nvm/fnm/asdf-installed agent would otherwise be "not found".
	binPath := r.binOverride
	if binPath == "" {
		binPath = resolveBinary(agent, shellEnv)
	}

	// The long-lived agent process is governed by `stop`/cancellation rather
	// than a fixed deadline, so it uses exec.Command (no context timeout).
	cmd := exec.Command(binPath, args...)
	cmd.Dir = worktreePath
	cmd.Env = composeEnv(shellEnv, token)
	// Start the agent as its own process-group leader so a separate `stop`
	// process can terminate it and all of its descendants together.
	cmd.SysProcAttr = process.SysProcAttr()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start agent process: %w", err)
	}

	// Persist the PID/PGID immediately so a concurrent `stop` can find the
	// agent while it runs. With Setpgid the new group id equals the child PID.
	pid := cmd.Process.Pid
	if err := r.workspaceManager.SetSessionProcess(workspaceID, string(agent), pid, pid, time.Now().UnixNano()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to persist agent process for %s: %v\n", workspaceID, err)
	}

	var sessionInfo SessionInfo
	var wg sync.WaitGroup

	// stdout parser: extract session id / usage and render or pass through.
	// ReadString has no line-length cap (unlike bufio.Scanner), so an oversized
	// agent line can never truncate the stream or stall the drain — which would
	// fill the pipe and hang the agent.
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(stdoutPipe)
		for {
			raw, err := reader.ReadString('\n')
			if len(raw) > 0 {
				line := strings.TrimRight(raw, "\r\n")
				var display string
				if agent == Codex {
					var msg map[string]interface{}
					if json.Unmarshal([]byte(line), &msg) == nil {
						display = captureCodex(msg, &sessionInfo)
					}
				} else {
					display = captureClaude(line, &sessionInfo)
				}
				if stream {
					// Raw passthrough: emit the original NDJSON/JSONL line.
					fmt.Fprint(os.Stdout, raw)
				} else if display != "" {
					fmt.Fprint(os.Stdout, display)
				}
			}
			if err != nil {
				return // EOF or read error: stdout drained
			}
		}
	}()

	// stderr forwarding (also uncapped, so a long stack trace cannot stall it).
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader := bufio.NewReader(stderrPipe)
		for {
			raw, err := reader.ReadString('\n')
			if len(raw) > 0 {
				fmt.Fprintln(os.Stderr, "[STDERR]", strings.TrimRight(raw, "\r\n"))
			}
			if err != nil {
				return
			}
		}
	}()

	// Drain stdout and stderr to EOF before reaping the process. cmd.Wait
	// closes the pipe read ends, which would truncate the final line (the
	// session id / usage) if it ran while the parser was still reading.
	wg.Wait()

	waitErr := cmd.Wait()

	// The agent has exited: persist the captured session/thread id and clear
	// the process record, but only if it still refers to our PID, so a
	// concurrent run for the same workspace is not clobbered.
	if err := r.workspaceManager.CompleteSession(workspaceID, string(agent), sessionInfo.SessionID, sessionInfo.ThreadID, pid); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to persist session for %s: %v\n", workspaceID, err)
	}

	if waitErr != nil {
		return &sessionInfo, fmt.Errorf("agent process finished with error: %w", waitErr)
	}

	return &sessionInfo, nil
}

// intField reads a numeric JSON field as int (JSON numbers decode to float64).
func intField(m map[string]interface{}, k string) int {
	v, _ := m[k].(float64)
	return int(v)
}

// claudeInputTokens is Claude's true input total: base input plus cache
// creation and cache read tokens. Reading input_tokens alone undercounts.
func claudeInputTokens(u map[string]interface{}) int {
	return intField(u, "input_tokens") +
		intField(u, "cache_creation_input_tokens") +
		intField(u, "cache_read_input_tokens")
}

// captureClaude parses one Claude NDJSON line, updates si, and returns the
// human-readable text to display for that line (empty when nothing to show).
func captureClaude(line string, si *SessionInfo) string {
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return ""
	}
	switch t, _ := msg["type"].(string); t {
	case "system":
		if si.SessionID == "" {
			if sid, ok := msg["session_id"].(string); ok {
				si.SessionID = sid
			}
		}
		if si.ThreadID == "" {
			if tid, ok := msg["thread_id"].(string); ok {
				si.ThreadID = tid
			}
		}
	case "assistant":
		return claudeAssistantText(msg)
	case "result":
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			si.Usage.InputTokens = claudeInputTokens(usage)
			si.Usage.OutputTokens = intField(usage, "output_tokens")
		}
		if model, ok := msg["model"].(string); ok {
			si.Usage.Model = model
		}
	}
	return ""
}

func claudeAssistantText(msg map[string]interface{}) string {
	message, ok := msg["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := message["content"].([]interface{})
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, block := range content {
		bl, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := bl["type"].(string); t == "text" {
			if text, ok := bl["text"].(string); ok {
				b.WriteString(text)
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "\n"
}

// captureCodex parses one Codex JSONL event, updates si, and returns the text
// to display for that event (empty when nothing to show).
func captureCodex(msg map[string]interface{}, si *SessionInfo) string {
	switch t, _ := msg["type"].(string); t {
	case "thread.started":
		if tid, ok := msg["thread_id"].(string); ok {
			si.ThreadID = tid
		}
	case "item.completed":
		item, ok := msg["item"].(map[string]interface{})
		if !ok {
			return ""
		}
		switch it, _ := item["type"].(string); it {
		case "agent_message":
			if text, ok := item["text"].(string); ok {
				return text + "\n"
			}
		case "command_execution":
			if cmdText, ok := item["command"].(string); ok {
				exit, _ := item["exit_code"].(float64)
				return fmt.Sprintf("[CMD] %s (exit: %.0f)\n", cmdText, exit)
			}
		case "mcp_tool_call":
			if tool, ok := item["tool"].(string); ok {
				return fmt.Sprintf("[MCP] Tool call: %s\n", tool)
			}
		}
	case "turn.completed":
		if usage, ok := msg["usage"].(map[string]interface{}); ok {
			si.Usage.InputTokens = intField(usage, "input_tokens")
			si.Usage.OutputTokens = intField(usage, "output_tokens")
		}
	}
	return ""
}
