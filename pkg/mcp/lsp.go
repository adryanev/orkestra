package mcp

import (
	"fmt"
	"os/exec"
	"time"
)

const ( lspTimeout = 5 * time.Second )

type lspHandle struct {
	cmd *exec.Cmd
	// TODO: add a mutex for concurrent access to stdin/stdout
}

// StartLsp spawns a gopls process for the given worktree path.
func StartLsp(worktreePath string) (*lspHandle, error) {
	cmd := exec.Command("gopls", "serve", "-rpc")
	// For some reason, gopls may not be in the PATH. Try a few alternative locations.
	cmd.Dir = worktreePath

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Run the command in the background
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start gopls: %w", err)
	}

	// TODO: read stdout line by line and parse JSON-RPC messages
	_ = stdin // Avoid unused variable error
	_ = stdout // Avoid unused variable error

	return &lspHandle{
		cmd: cmd,
	}, nil
}

// CallLsp sends a JSON-RPC request to the LSP server and waits for a response.
// handle: The LSP handle obtained from StartLsp.
// method: The LSP method to call (e.g., "textDocument/definition").
// params: The parameters for the LSP method.
func CallLsp(handle *lspHandle, method string, params interface{}) (interface{}, error) {
	// TODO: implement JSON-RPC request formatting, sending over stdin, and response parsing from stdout
	_ = handle
	_ = method
	_ = params
	return nil, fmt.Errorf("not implemented")
}

// LSP tool implementations

// lsp_goto_definition finds the definition of a symbol at a given position.
func lsp_goto_definition(filePath string, line, character int) (interface{}, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}

// lsp_hover retrieves hover information for a symbol at a given position.
func lsp_hover(filePath string, line, character int) (interface{}, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}

// lsp_references finds all references to a symbol at a given position.
func lsp_references(filePath string, line, character int) (interface{}, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}

// lsp_diagnostics retrieves diagnostics (errors, warnings) for a file.
func lsp_diagnostics(filePath string) (interface{}, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}

// lsp_rename renames a symbol across the workspace.
func lsp_rename(filePath string, line, character int, newName string) (interface{}, error) {
	// TODO: implement
	return nil, fmt.Errorf("not implemented")
}
