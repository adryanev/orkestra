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

	"github.com/adryanev/orkestra/pkg/env"
	"github.com/adryanev/orkestra/pkg/gitauth"
	"github.com/adryanev/orkestra/pkg/process"
	"github.com/adryanev/orkestra/pkg/workspace"
)

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
// invocation. Pulled out of executeAgent so the resume/session wiring is unit
// testable without spawning a process.
func buildAgentArgs(agent AgentType, prompt, resumeSessionID, resumeThreadID string) (string, []string, error) {
	switch agent {
	case Claude:
		args := []string{"--output-format", "stream-json", "--verbose", "-p", prompt}
		// Continue the prior session when a session id is known.
		if resumeSessionID != "" {
			args = append(args, "--resume", resumeSessionID)
		}
		args = append(args, "--permission-mode", "bypassPermissions")
		args = append(args, "--disallowedTools", "EnterWorktree,ExitWorktree")
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
	_, args, err := buildAgentArgs(agent, prompt, resumeSessionID, resumeThreadID)
	if err != nil {
		return nil, err
	}

	shellEnv := env.Captured()
	// Resolve the agent binary against the captured shell PATH. exec.Command
	// resolves bare names against orkestra's own PATH, not cmd.Env, so an
	// nvm/fnm/asdf-installed agent would otherwise be "not found".
	binPath := resolveBinary(agent, shellEnv)

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			var display string
			if agent == Codex {
				var msg map[string]interface{}
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					continue
				}
				display = captureCodex(msg, &sessionInfo)
			} else {
				display = captureClaude(line, &sessionInfo)
			}
			if stream {
				// Raw passthrough: emit the original NDJSON/JSONL line.
				fmt.Fprintln(os.Stdout, line)
			} else if display != "" {
				fmt.Fprint(os.Stdout, display)
			}
		}
	}()

	// stderr forwarding.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			fmt.Fprintln(os.Stderr, "[STDERR]", scanner.Text())
		}
	}()

	// Drain stdout and stderr to EOF before reaping the process. cmd.Wait
	// closes the pipe read ends, which would truncate the final line (the
	// session id / usage) if it ran while the parser was still reading.
	wg.Wait()

	waitErr := cmd.Wait()

	// The agent has exited: persist the captured session/thread id and clear
	// the process record (AddSession writes zero-value PID/PGID) so a later
	// `stop` treats the workspace as already stopped.
	if err := r.workspaceManager.AddSession(workspace.Session{
		WorkspaceID: workspaceID,
		Agent:       string(agent),
		SessionID:   sessionInfo.SessionID,
		ThreadID:    sessionInfo.ThreadID,
	}); err != nil {
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
