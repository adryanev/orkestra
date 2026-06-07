package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// mcpConfigFile is the structure Claude's --mcp-config flag expects.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// ClaudeConfig builds the MCP config naming the orkestra stdio server bound to
// workspaceID. orkestraBin is the command the agent runs to start the server
// (its own absolute path), so a nvm/asdf install resolves correctly.
func ClaudeConfig(workspaceID, orkestraBin string) mcpConfigFile {
	return mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{
			"orkestra": {
				Type:    "stdio",
				Command: orkestraBin,
				Args:    []string{"mcp", "--workspace", workspaceID},
			},
		},
	}
}

// WriteClaudeConfig writes the MCP config for workspaceID under
// configDir/mcp/<workspace-id>.json and returns the path.
func WriteClaudeConfig(configDir, workspaceID, orkestraBin string) (string, error) {
	cfg := ClaudeConfig(workspaceID, orkestraBin)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, workspaceID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// CodexServerName returns the prefixed, collision-resistant server name used
// when registering orkestra with `codex mcp add`.
func CodexServerName(workspaceID string) string {
	return fmt.Sprintf("orkestra_%s", workspaceID)
}
