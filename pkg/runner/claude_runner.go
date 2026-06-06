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
	switch agent {
	case Claude:
		cmdName = "claude"
		args = append([]string{"--output-format", "stream-json", "--verbose"}, args...)
		// Ensure necessary flags are present
		hasPermissionMode := false
		hasDisallowed := false
		for _, arg := range args {
			if strings.HasPrefix(arg, "--permission-mode") {
				hasPermissionMode = true
			}
			if strings.HasPrefix(arg, "--disallowed-tools") || strings.HasPrefix(arg, "--disallowedTools") {
				hasDisallowed = true
			}
		}
		if !hasPermissionMode {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
		if !hasDisallowed {
			args = append(args, "--disallowedTools", "EnterWorktree,ExitWorktree")
		}
	case Codex:
		cmdName = "codex"
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
	var firstLine = true

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println("[Agent STDOUT]:", line)

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
					}
					if tid, ok := msg["thread_id"].(string); ok {
						sessionInfo.ThreadID = tid
					}
					firstLine = false
				}
			case "result":
				if usageData, ok := msg["usage"].(map[string]interface{}); ok {
					usageJSON, _ := json.Marshal(usageData)
					json.Unmarshal(usageJSON, &usageReport)
				}
				sessionInfo.Usage = usageReport
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println("[Agent STDERR]:", line)
		}
	}()

	if err := cmd.Wait(); err != nil {
		return &sessionInfo, fmt.Errorf("agent process finished with error: %w", err)
	}

	return &sessionInfo, nil
}