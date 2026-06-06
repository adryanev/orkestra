package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

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

// Run executes an agent command in a given workspace.
func (r *Runner) Run(workspaceID string, agent AgentType, prompt string, resume bool) (*SessionInfo, error) {
	r.Lock()
	defer r.Unlock()

	ws, err := r.workspaceManager.GetWorkspace(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace %s: %w", workspaceID, err)
	}

	cmdArgs := []string{"-p", prompt}
	if resume {
		cmdArgs = append(cmdArgs, "--resume")
	}

	sessionInfo, err := r.executeAgent(ws.WorktreePath, agent, cmdArgs)
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

func (r *Runner) executeAgent(worktreePath string, agent AgentType, args []string) (*SessionInfo, error) {
	var cmdName string
	codexResume := false

	switch agent {
	case Claude:
		cmdName = "claude"
		// Claude: -p is already in args. Add stream-json flags.
		args = append([]string{"--output-format", "stream-json", "--verbose"}, args...)
		hasPermission := false
		hasDisallowed := false
		for _, a := range args {
			if strings.HasPrefix(a, "--permission-mode") {
				hasPermission = true
			}
			if strings.HasPrefix(a, "--disallowed") {
				hasDisallowed = true
			}
		}
		if !hasPermission {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
		if !hasDisallowed {
			args = append(args, "--disallowedTools", "EnterWorktree,ExitWorktree")
		}

	case Codex:
		cmdName = "codex"
		// Check if this is a resume: args contains "--resume"
		for _, a := range args {
			if a == "--resume" {
				codexResume = true
				break
			}
		}
		// Extract the prompt (last arg)
		prompt := args[len(args)-1]

		if codexResume {
			// codex exec resume <thread_id> --json "prompt"
			// Need thread_id from saved session
			args = []string{"exec", "resume"}
			args = append(args, prompt) // thread_id comes first after "resume" via session lookup
		} else {
			args = []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox", prompt}
		}

	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agent)
	}

	cmd := exec.Command(cmdName, args...)
	cmd.Dir = worktreePath

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