package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"

	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/adryanev/orkestra/pkg/env"
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

// Run executes an agent command in a given workspace.
func (r *Runner) Run(workspaceID string, agent AgentType, prompt string, resume bool, stream bool) (*SessionInfo, error) {
	r.Lock()
	defer r.Unlock()

	ws, err := r.workspaceManager.GetWorkspace(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace %s: %w", workspaceID, err)
	}

	// On resume, look up the saved session/thread id so it can be passed to
	// the agent. Without it, `--resume` has nothing to continue.
	var resumeSessionID, resumeThreadID string
	if resume {
		s, err := r.workspaceManager.GetSession(workspaceID)
		if err != nil {
			return nil, fmt.Errorf("cannot resume workspace %s: no saved session (run it first): %w", workspaceID, err)
		}
		resumeSessionID = s.SessionID
		resumeThreadID = s.ThreadID
	}

	sessionInfo, err := r.executeAgent(ws.WorktreePath, agent, prompt, resumeSessionID, resumeThreadID, stream)
	if err != nil {
		return nil, fmt.Errorf("agent execution failed for workspace %s: %w", workspaceID, err)
	}

	// Update workspace status
	if err := r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "active"); err != nil {
		fmt.Printf("Warning: failed to update workspace status for %s: %v\n", workspaceID, err)
	}

	return sessionInfo, nil
}

// Stop terminates the agent process for a given workspace.
func (r *Runner) Stop(workspaceID string) error {
	r.Lock()
	defer r.Unlock()

	if err := r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "inactive"); err != nil {
		return fmt.Errorf("failed to update workspace status for %s: %w", workspaceID, err)
	}

	return nil
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

func (r *Runner) executeAgent(worktreePath string, agent AgentType, prompt, resumeSessionID, resumeThreadID string, stream bool) (*SessionInfo, error) {
	cmdName, args, err := buildAgentArgs(agent, prompt, resumeSessionID, resumeThreadID)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(cmdName, args...)
	cmd.Dir = worktreePath

	// Inject captured shell environment
	shellEnv := env.Captured()
	for k, v := range shellEnv.AllVars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

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

	var sessionInfo SessionInfo
	var usageReport UsageReport

	if agent == Codex {
		// Codex JSONL parser
		go func() {
			decoder := json.NewDecoder(stdoutPipe)
			for {
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err != nil {
					break
				}

				var msg map[string]interface{}
				if err := json.Unmarshal(raw, &msg); err != nil {
					continue
				}

				eventType, _ := msg["type"].(string)

				switch eventType {
				case "thread.started":
					if tid, ok := msg["thread_id"].(string); ok {
						sessionInfo.ThreadID = tid
						fmt.Printf("[ORKESTRA] Thread started: %s\n", tid)
					}
				case "item.completed":
					if item, ok := msg["item"].(map[string]interface{}); ok {
						itemType, _ := item["type"].(string)
						switch itemType {
						case "agent_message":
							if text, ok := item["text"].(string); ok {
								fmt.Println(text)
							}
						case "command_execution":
							if cmdText, ok := item["command"].(string); ok {
								exitCode, _ := item["exit_code"].(float64)
								fmt.Printf("[CMD] %s (exit: %.0f)\n", cmdText, exitCode)
							}
						case "mcp_tool_call":
							if tool, ok := item["tool"].(string); ok {
								fmt.Printf("[MCP] Tool call: %s\n", tool)
							}
						}
					}
				case "turn.completed":
					if usage, ok := msg["usage"].(map[string]interface{}); ok {
						usageJSON, _ := json.Marshal(usage)
						json.Unmarshal(usageJSON, &usageReport)
						sessionInfo.Usage = usageReport
					}
					fmt.Println("[ORKESTRA] Turn completed")
				case "turn.failed":
					fmt.Println("[ORKESTRA] Turn failed")
				}
			}
		}()
	} else {
		// Claude NDJSON parser
		var firstLine = true
		go func() {
			scanner := bufio.NewScanner(stdoutPipe)
			for scanner.Scan() {
				line := scanner.Text()

				var msg map[string]interface{}
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					continue
				}

				msgType, ok := msg["type"].(string)
				if !ok {
					continue
				}

				switch msgType {
				case "system":
					if firstLine {
						if sid, ok := msg["session_id"].(string); ok {
							sessionInfo.SessionID = sid
							fmt.Printf("[ORKESTRA] Session: %s\n", sid)
						}
						if tid, ok := msg["thread_id"].(string); ok {
							sessionInfo.ThreadID = tid
						}
						firstLine = false
					}
				case "assistant":
					if message, ok := msg["message"].(map[string]interface{}); ok {
						if content, ok := message["content"].([]interface{}); ok {
							for _, block := range content {
								if b, ok := block.(map[string]interface{}); ok {
									if bType, _ := b["type"].(string); bType == "text" {
										if text, ok := b["text"].(string); ok {
											fmt.Print(text)
										}
									}
								}
							}
							fmt.Println()
						}
					}
				case "result":
					if usage, ok := msg["usage"].(map[string]interface{}); ok {
						usageJSON, _ := json.Marshal(usage)
						json.Unmarshal(usageJSON, &usageReport)
						sessionInfo.Usage = usageReport
					}
				}
			}
		}()
	}

	// stderr forwarding regardless of agent type
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			fmt.Println("[STDERR]", scanner.Text())
		}
	}()

	if err := cmd.Wait(); err != nil {
		return &sessionInfo, fmt.Errorf("agent process finished with error: %w", err)
	}

	return &sessionInfo, nil
}