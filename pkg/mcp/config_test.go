package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeConfigPreservesExistingServers(t *testing.T) {
	existing := &mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{
			"db": {
				Type:    "stdio",
				Command: "db-mcp",
				Args:    []string{"--port", "5432"},
			},
			"browser": {
				Type:    "stdio",
				Command: "playwright-mcp",
			},
		},
	}
	merged := MergeConfig(existing, "ws-1", "/orkestra")

	if len(merged.MCPServers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(merged.MCPServers))
	}
	// Existing servers preserved.
	if _, ok := merged.MCPServers["db"]; !ok {
		t.Error("db server missing from merged config")
	}
	if _, ok := merged.MCPServers["browser"]; !ok {
		t.Error("browser server missing from merged config")
	}
	// Orkestra added.
	ork, ok := merged.MCPServers["orkestra"]
	if !ok {
		t.Fatal("orkestra server missing from merged config")
	}
	if ork.Command != "/orkestra" {
		t.Errorf("orkestra command = %q, want /orkestra", ork.Command)
	}
}

func TestMergeConfigDoesNotOverwriteExistingOrkestra(t *testing.T) {
	existing := &mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{
			"orkestra": {
				Type:    "stdio",
				Command: "/custom/orkestra",
				Args:    []string{"mcp", "--workspace", "custom"},
			},
		},
	}
	merged := MergeConfig(existing, "ws-1", "/orkestra")

	// Should NOT be overwritten by the default orkestra server.
	ork := merged.MCPServers["orkestra"]
	if ork.Command != "/custom/orkestra" {
		t.Errorf("orkestra command = %q, want /custom/orkestra (should not overwrite)", ork.Command)
	}
	if len(merged.MCPServers) != 1 {
		t.Errorf("expected 1 server (existing orkestra only), got %d", len(merged.MCPServers))
	}
}

func TestMergeConfigWithNilExisting(t *testing.T) {
	merged := MergeConfig(nil, "ws-1", "/orkestra")

	if len(merged.MCPServers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(merged.MCPServers))
	}
	ork := merged.MCPServers["orkestra"]
	if ork.Command != "/orkestra" {
		t.Errorf("orkestra command = %q, want /orkestra", ork.Command)
	}
	wantArgs := []string{"mcp", "--workspace", "ws-1"}
	for i := range wantArgs {
		if ork.Args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, ork.Args[i], wantArgs[i])
		}
	}
}

func TestDiscoverClaudeConfigsReturnsNilWhenNoFiles(t *testing.T) {
	// A temp dir with no claude config should produce nil.
	root := t.TempDir()
	cfg := DiscoverClaudeConfigs(root)
	if cfg != nil {
		t.Error("expected nil when no config exists")
	}
}

func TestDiscoverClaudeConfigsFindsProjectLocal(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgData := `{"mcpServers":{"custom":{"type":"stdio","command":"custom"}}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), []byte(cfgData), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DiscoverClaudeConfigs(root)
	if cfg == nil {
		t.Fatal("expected config to be found")
	}
	if _, ok := cfg.MCPServers["custom"]; !ok {
		t.Error("custom server not found in discovered config")
	}
}

func TestDiscoverClaudeConfigsPrefersProjectLocalOverUser(t *testing.T) {
	root := t.TempDir()
	// Project local (highest priority)
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectCfg := `{"mcpServers":{"project":{"type":"stdio","command":"project"}}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), []byte(projectCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DiscoverClaudeConfigs(root)
	if cfg == nil {
		t.Fatal("expected config to be found")
	}
	if _, ok := cfg.MCPServers["project"]; !ok {
		t.Error("project server not found")
	}
}

func TestDiscoverClaudeConfigsSkipsInvalidJSON(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid JSON should be skipped.
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DiscoverClaudeConfigs(root)
	if cfg != nil {
		t.Error("expected nil when config has invalid JSON")
	}
}

func TestDiscoverClaudeConfigsSkipsEmptyServers(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty mcpServers should be skipped.
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DiscoverClaudeConfigs(root)
	if cfg != nil {
		t.Error("expected nil when config has empty mcpServers")
	}
}

func TestWriteClaudeConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteClaudeConfig(dir, "ws-7", "/bin/orkestra", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "mcp", "ws-7.json") {
		t.Errorf("path = %q, want mcp/ws-7.json under config dir", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got mcpConfigFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config did not round-trip: %v", err)
	}
	args := got.MCPServers["orkestra"].Args
	if len(args) < 3 {
		t.Fatalf("args = %v, want at least 3 elements", args)
	}
	if args[2] != "ws-7" {
		t.Errorf("round-tripped workspace id = %q, want ws-7", args[2])
	}
}

func TestWriteClaudeConfigWithDiscoveryMerges(t *testing.T) {
	dir := t.TempDir()

	// Set up a project-local Claude config.
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userCfg := `{"mcpServers":{"custom":{"type":"stdio","command":"my-tool"}}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "mcp.json"), []byte(userCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write the merged config with discovery=true, using the parent dir
	// as worktreeRoot so it finds the .claude/mcp.json.
	path, err := WriteClaudeConfig(dir, "ws-m", "/orkestra", true, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "mcp/ws-m.json") {
		t.Errorf("path = %q, want .../mcp/ws-m.json", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got mcpConfigFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config did not round-trip: %v", err)
	}
	// Both servers present.
	if _, ok := got.MCPServers["custom"]; !ok {
		t.Error("custom server should be present in merged config")
	}
	if _, ok := got.MCPServers["orkestra"]; !ok {
		t.Error("orkestra server should be present in merged config")
	}
	if len(got.MCPServers) != 2 {
		t.Errorf("expected 2 servers, got %d: %v", len(got.MCPServers), got.MCPServers)
	}
}

func TestWriteClaudeConfigWithDiscoveryNoExisting(t *testing.T) {
	dir := t.TempDir()

	// No existing Claude config anywhere, discovery should produce
	// just the orkestra server.
	path, err := WriteClaudeConfig(dir, "ws-n", "/orkestra", true, dir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got mcpConfigFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config did not round-trip: %v", err)
	}
	// Only orkestra.
	if _, ok := got.MCPServers["orkestra"]; !ok {
		t.Error("orkestra server should be present")
	}
	if len(got.MCPServers) != 1 {
		t.Errorf("expected 1 server, got %d", len(got.MCPServers))
	}
}

func TestCodexServerNameIsPrefixed(t *testing.T) {
	if got := CodexServerName("abc"); got != "orkestra_abc" {
		t.Errorf("CodexServerName = %q, want orkestra_abc", got)
	}
}