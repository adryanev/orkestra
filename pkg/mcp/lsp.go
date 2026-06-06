package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// LspHandle manages a per-workspace LSP server process (gopls).
type LspHandle struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	id     int
}

// StartLsp starts gopls in the given worktree directory.
// Sends initialize request and waits for response.
func StartLsp(worktreePath string) (*LspHandle, error) {
	cmd := exec.Command("gopls", "serve")
	cmd.Dir = worktreePath

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create LSP stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create LSP stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start gopls: %w", err)
	}

	h := &LspHandle{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		id:     1,
	}

	// Send initialize request
	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"processId": nil,
			"capabilities": map[string]interface{}{},
			"rootUri": fmt.Sprintf("file://%s", worktreePath),
		},
	}
	if _, err := h.call("initialize", initReq["params"]); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("LSP initialize failed: %w", err)
	}

	// Send initialized notification
	fmt.Fprintf(stdin, `{"jsonrpc":"2.0","method":"initialized","params":{}}`+"\n")

	return h, nil
}

// CallLsp sends a JSON-RPC request and returns the result.
func (h *LspHandle) CallLsp(method string, params interface{}) (map[string]interface{}, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.id++
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      h.id,
		"method":  method,
		"params":  params,
	}

	reqBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal LSP request: %w", err)
	}

	if _, err := fmt.Fprintf(h.stdin, "%s\n", string(reqBytes)); err != nil {
		return nil, fmt.Errorf("failed to write LSP request: %w", err)
	}

	// Read response
	scanner := bufio.NewScanner(h.stdout)
	scanner.Buffer(make([]byte, 0, 65536), 65536)

	for scanner.Scan() {
		line := scanner.Text()
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		// Check if this response matches our request ID
		if id, ok := msg["id"]; ok {
			switch v := id.(type) {
			case float64:
				if int(v) == h.id {
					if errVal, ok := msg["error"]; ok {
						return nil, fmt.Errorf("LSP error: %v", errVal)
					}
					result, _ := msg["result"].(map[string]interface{})
					return result, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("LSP response not received")
}

// Close kills the LSP process.
func (h *LspHandle) Close() {
	h.cmd.Process.Kill()
	h.cmd.Wait()
}

func (h *LspHandle) call(method string, params interface{}) (map[string]interface{}, error) {
	return h.CallLsp(method, params)
}

// LspManager manages LSP handles across workspaces.
type LspManager struct {
	mu       sync.Mutex
	handles  map[string]*LspHandle
}

func NewLspManager() *LspManager {
	return &LspManager{
		handles: make(map[string]*LspHandle),
	}
}

func (m *LspManager) GetOrStart(worktreePath, workspaceID string) (*LspHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.handles[workspaceID]; ok {
		return h, nil
	}

	h, err := StartLsp(worktreePath)
	if err != nil {
		return nil, err
	}
	m.handles[workspaceID] = h
	return h, nil
}

func (m *LspManager) Stop(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.handles[workspaceID]; ok {
		h.Close()
		delete(m.handles, workspaceID)
	}
}

// LSP tool helpers

func lspGotoDefinition(handle *LspHandle, filePath string, line, character int) (interface{}, error) {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", filePath),
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": character - 1,
		},
	}
	result, err := handle.CallLsp("textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func lspHover(handle *LspHandle, filePath string, line, character int) (interface{}, error) {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", filePath),
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": character - 1,
		},
	}
	result, err := handle.CallLsp("textDocument/hover", params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func lspReferences(handle *LspHandle, filePath string, line, character int) (interface{}, error) {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", filePath),
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": character - 1,
		},
		"context": map[string]interface{}{
			"includeDeclaration": true,
		},
	}
	result, err := handle.CallLsp("textDocument/references", params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func lspDiagnostics(handle *LspHandle, filePath string) (interface{}, error) {
	// gopls publishes diagnostics via textDocument/publishDiagnostics notification
	// We trigger a refresh
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", filePath),
		},
	}
	_, err := handle.CallLsp("textDocument/diagnostic", params)
	if err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func lspRename(handle *LspHandle, filePath string, line, character int, newName string) (interface{}, error) {
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": fmt.Sprintf("file://%s", filePath),
		},
		"position": map[string]interface{}{
			"line":      line - 1,
			"character": character - 1,
		},
		"newName": newName,
	}
	result, err := handle.CallLsp("textDocument/rename", params)
	if err != nil {
		return nil, err
	}
	return result, nil
}