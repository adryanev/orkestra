package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/adryanev/orkestra/pkg/workspace"
)

// Server handles MCP communication over stdio.
type Server struct {
	workspaceManager *workspace.Manager
	lspManager       *LspManager
	stdin            *bufio.Scanner
	stdout           io.Writer
	mu               sync.Mutex
	active           bool
}

// NewServer creates a new MCP server.
func NewServer(wm *workspace.Manager) *Server {
	return &Server{
		workspaceManager: wm,
		lspManager:       NewLspManager(),
		stdin:            bufio.NewScanner(os.Stdin),
		stdout:           os.Stdout,
		active:           true,
	}
}

// Listen starts the MCP server's main loop.
func (s *Server) Listen() {
	s.mu.Lock()
	s.active = true
	s.mu.Unlock()

	for s.active && s.stdin.Scan() {
		line := s.stdin.Text()
		var request map[string]interface{}

		err := json.Unmarshal([]byte(line), &request)
		if err != nil {
			s.sendResponse(nil, fmt.Sprintf("invalid JSON received: %v", err))
			continue
		}

		toolName, ok := request["tool_name"].(string)
		if !ok {
			s.sendResponse(nil, "missing 'tool_name' in request")
			continue
		}

		args, _ := request["arguments"].(map[string]interface{})

		var result interface{}
		var errResp error

		switch toolName {
		case "get_workspace_info":
			result, errResp = s.getWorkspaceInfo(args)
		case "rename_branch":
			result, errResp = s.renameBranch(args)
		case "notify":
			result, errResp = s.notify(args)
		// LSP Tools Registration
		case "lsp_goto_definition":
			filePath, _ := args["file_path"].(string)
			line, _ := args["line"].(float64)
			character, _ := args["character"].(float64)
			wsID, _ := args["workspace_id"].(string)
			handle, err := s.lspManager.GetOrStart("", wsID)
			if err != nil {
				errResp = err
			} else {
				result, errResp = lspGotoDefinition(handle, filePath, int(line), int(character))
			}
		case "lsp_hover":
			filePath, _ := args["file_path"].(string)
			line, _ := args["line"].(float64)
			character, _ := args["character"].(float64)
			wsID, _ := args["workspace_id"].(string)
			handle, err := s.lspManager.GetOrStart("", wsID)
			if err != nil {
				errResp = err
			} else {
				result, errResp = lspHover(handle, filePath, int(line), int(character))
			}
		case "lsp_references":
			filePath, _ := args["file_path"].(string)
			line, _ := args["line"].(float64)
			character, _ := args["character"].(float64)
			wsID, _ := args["workspace_id"].(string)
			handle, err := s.lspManager.GetOrStart("", wsID)
			if err != nil {
				errResp = err
			} else {
				result, errResp = lspReferences(handle, filePath, int(line), int(character))
			}
		case "lsp_diagnostics":
			filePath, _ := args["file_path"].(string)
			wsID, _ := args["workspace_id"].(string)
			handle, err := s.lspManager.GetOrStart("", wsID)
			if err != nil {
				errResp = err
			} else {
				result, errResp = lspDiagnostics(handle, filePath)
			}
		case "lsp_rename":
			filePath, _ := args["file_path"].(string)
			line, _ := args["line"].(float64)
			character, _ := args["character"].(float64)
			newName, _ := args["new_name"].(string)
			wsID, _ := args["workspace_id"].(string)
			handle, err := s.lspManager.GetOrStart("", wsID)
			if err != nil {
				errResp = err
			} else {
				result, errResp = lspRename(handle, filePath, int(line), int(character), newName)
			}
		default:
			errResp = fmt.Errorf("unknown tool: %s", toolName)
		}

		if errResp != nil {
			s.sendResponse(nil, errResp.Error())
		} else {
			s.sendResponse(result, "")
		}
	}

	if err := s.stdin.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP stdin error: %v\n", err)
	}
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
}

// Stop terminates the MCP server.
func (s *Server) Stop() {
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
	// Signal stdin to close if needed, or rely on the process exit usually handles this.
}

func (s *Server) sendResponse(result interface{}, errMsg string) {
	response := map[string]interface{}{
		"result": result,
		"error":  errMsg,
	}
	responseJSON, _ := json.Marshal(response)
	fmt.Fprintln(s.stdout, string(responseJSON))
}

// Tool Implementations

func (s *Server) getWorkspaceInfo(args map[string]interface{}) (interface{}, error) {
	id, ok := args["id"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'id' argument for get_workspace_info")
	}

	ws, err := s.workspaceManager.GetWorkspace(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace info: %w", err)
	}

	return map[string]string{
		"id":           ws.ID,
		"name":         ws.Name,
		"repo_path":    ws.RepoPath,
		"worktree_path": ws.WorktreePath,
		"branch":       ws.Branch,
		"status":       ws.Status,
	}, nil
}

func (s *Server) renameBranch(args map[string]interface{}) (interface{}, error) {
	id, ok := args["id"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'id' argument for rename_branch")
	}

	newBranch, ok := args["new_branch"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'new_branch' argument for rename_branch")
	}

	ws, err := s.workspaceManager.GetWorkspace(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace for renaming: %w", err)
	}

	// Actually rename git branch
	cmd := exec.Command("git", "branch", "-m", newBranch)
	cmd.Dir = ws.WorktreePath
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to rename git branch: %w", err)
	}

	ws.Branch = newBranch
	if err := s.workspaceManager.Save(); err != nil {
		return nil, fmt.Errorf("failed to save workspace after branch rename: %w", err)
	}

	return map[string]string{
		"message": fmt.Sprintf("Branch for workspace %s renamed to %s", id, newBranch),
	}, nil
}

func (s *Server) notify(args map[string]interface{}) (interface{}, error) {
	id, ok := args["id"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'id' argument for notify")
	}

	message, ok := args["message"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'message' argument for notify")
	}

	// TODO: Implement notification logic (e.g., writing to a workspace-specific log file)
	fmt.Printf("[Workspace Notification - %s]: %s\n", id, message)

	return map[string]string{
		"message": fmt.Sprintf("Notification sent for workspace %s", id),
	}, nil
}
