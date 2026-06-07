package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeConfigNamesOrkestraServer(t *testing.T) {
	cfg := ClaudeConfig("ws-42", "/usr/local/bin/orkestra")
	entry, ok := cfg.MCPServers["orkestra"]
	if !ok {
		t.Fatal("config must name the orkestra server")
	}
	if entry.Type != "stdio" {
		t.Errorf("type = %q, want stdio", entry.Type)
	}
	if entry.Command != "/usr/local/bin/orkestra" {
		t.Errorf("command = %q, want the orkestra binary path", entry.Command)
	}
	want := []string{"mcp", "--workspace", "ws-42"}
	if len(entry.Args) != len(want) {
		t.Fatalf("args = %v, want %v", entry.Args, want)
	}
	for i := range want {
		if entry.Args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, entry.Args[i], want[i])
		}
	}
}

func TestWriteClaudeConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteClaudeConfig(dir, "ws-7", "/bin/orkestra")
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

func TestCodexServerNameIsPrefixed(t *testing.T) {
	if got := CodexServerName("abc"); got != "orkestra_abc" {
		t.Errorf("CodexServerName = %q, want orkestra_abc", got)
	}
}
