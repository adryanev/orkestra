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

// DiscoverClaudeConfigs probes the standard Claude MCP config locations
// in priority order and returns the content of the highest-priority file
// that exists and is valid JSON. Returns nil if no config is found.
// worktreeRoot is the git worktree path used to resolve project-local
// (.claude/mcp.json) paths.
func DiscoverClaudeConfigs(worktreeRoot string) *mcpConfigFile {
	// Priority order: project-local > ~/.claude > ~/.config/claude
	candidates := []string{}

	if worktreeRoot != "" {
		candidates = append(candidates, filepath.Join(worktreeRoot, ".claude", "mcp.json"))
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".claude", "mcp.json"),
			filepath.Join(home, ".config", "claude", "mcp.json"),
		)
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg mcpConfigFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		if len(cfg.MCPServers) > 0 {
			return &cfg
		}
	}
	return nil
}

// MergeConfig inserts the orkestra MCP server into an existing config.
// It never overwrites an existing "orkestra" entry. Returns a new config
// with the orkestra server added alongside any existing servers.
func MergeConfig(existing *mcpConfigFile, workspaceID, orkestraBin string) mcpConfigFile {
	merged := mcpConfigFile{
		MCPServers: make(map[string]mcpServerEntry),
	}

	// Copy existing servers.
	if existing != nil {
		for k, v := range existing.MCPServers {
			merged.MCPServers[k] = v
		}
	}

	// Add orkestra server (don't overwrite if user already has one named "orkestra").
	if _, exists := merged.MCPServers["orkestra"]; !exists {
		merged.MCPServers["orkestra"] = mcpServerEntry{
			Type:    "stdio",
			Command: orkestraBin,
			Args:    []string{"mcp", "--workspace", workspaceID},
		}
	}

	return merged
}

// WriteClaudeConfig writes the MCP config for workspaceID under
// configDir/mcp/<workspace-id>.json and returns the path.
// If discovery is true, it probes the user's existing Claude MCP config
// and merges the orkestra server into it rather than replacing it.
func WriteClaudeConfig(configDir, workspaceID, orkestraBin string, discovery bool, worktreeRoot string) (string, error) {
	var cfg mcpConfigFile

	if discovery {
		existing := DiscoverClaudeConfigs(worktreeRoot)
		cfg = MergeConfig(existing, workspaceID, orkestraBin)
	} else {
		cfg = mcpConfigFile{
			MCPServers: map[string]mcpServerEntry{
				"orkestra": {
					Type:    "stdio",
					Command: orkestraBin,
					Args:    []string{"mcp", "--workspace", workspaceID},
				},
			},
		}
	}

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
