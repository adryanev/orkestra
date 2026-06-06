package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/adryanev/orkestra/pkg/workspace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestWorkspace creates a manager with one registered workspace whose
// worktree is a writable temp directory (no real git worktree needed for the
// tool tests), and returns the manager and the workspace id.
func newTestWorkspace(t *testing.T) (*workspace.Manager, string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XORKESTRA_HOME", cfg)
	worktree := filepath.Join(cfg, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	// Register the workspace directly via the registry file. There is no
	// CreateWorkspace here because that needs a real git repo; the MCP tools
	// only read the registry and the worktree path.
	ws := workspace.Workspace{ID: "ws-test", Name: "test", RepoPath: cfg, WorktreePath: worktree, Branch: "main", Status: "active"}
	writeWorkspaceRegistry(t, cfg, ws)
	m, err := workspace.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m, ws.ID
}

func newTwoTestWorkspaces(t *testing.T) (*workspace.Manager, string, string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XORKESTRA_HOME", cfg)
	worktreeA := filepath.Join(cfg, "wt-a")
	worktreeB := filepath.Join(cfg, "wt-b")
	if err := os.MkdirAll(worktreeA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeB, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{
  "ws-a":{"id":"ws-a","name":"a","repo_path":"` + cfg + `","worktree_path":"` + worktreeA + `","branch":"main","status":"active"},
  "ws-b":{"id":"ws-b","name":"b","repo_path":"` + cfg + `","worktree_path":"` + worktreeB + `","branch":"main","status":"active"}
}`
	if err := os.WriteFile(filepath.Join(cfg, "workspaces.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := workspace.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m, "ws-a", "ws-b"
}

// writeWorkspaceRegistry writes a workspaces.json containing ws.
func writeWorkspaceRegistry(t *testing.T, cfg string, ws workspace.Workspace) {
	t.Helper()
	// Use the manager's own JSON shape: map keyed by id.
	content := `{"` + ws.ID + `":{"id":"` + ws.ID + `","name":"` + ws.Name +
		`","repo_path":"` + ws.RepoPath + `","worktree_path":"` + ws.WorktreePath +
		`","branch":"` + ws.Branch + `","status":"` + ws.Status + `"}}`
	if err := os.WriteFile(filepath.Join(cfg, "workspaces.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// connect wires an in-memory client to the server under test.
func connect(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := s.buildServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestToolsListAdvertisesExpectedTools(t *testing.T) {
	m, id := newTestWorkspace(t)
	s, err := NewServer(m, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, s)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{
		"get_workspace_info", "lsp_diagnostics", "lsp_find_references",
		"lsp_goto_definition", "lsp_hover", "lsp_rename", "lsp_workspace_symbols",
		"notify", "rename_branch",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

func TestGetWorkspaceInfoTool(t *testing.T) {
	m, id := newTestWorkspace(t)
	s, err := NewServer(m, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, s)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_workspace_info",
		Arguments: map[string]any{"id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["branch"] != "main" {
		t.Errorf("branch = %v, want main", sc["branch"])
	}

	// Unknown id is a tool error, not a panic.
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_workspace_info",
		Arguments: map[string]any{"id": "nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bad.IsError {
		t.Error("expected a tool error for an unknown workspace id")
	}
}

func TestWorkspaceToolsRejectDifferentBoundWorkspace(t *testing.T) {
	m, boundID, otherID := newTwoTestWorkspaces(t)
	s, err := NewServer(m, boundID, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, s)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_workspace_info",
		Arguments: map[string]any{"id": otherID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected bound server to reject a different workspace id")
	}
}

func TestNotifyToolWritesLog(t *testing.T) {
	m, id := newTestWorkspace(t)
	s, err := NewServer(m, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, s)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "notify",
		Arguments: map[string]any{"id": id, "message": "build finished"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("notify returned error: %+v", res.Content)
	}
	logPath := filepath.Join(m.ConfigDir(), "logs", id+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log not written: %v", err)
	}
	if !strings.Contains(string(data), "build finished") {
		t.Errorf("log missing message: %q", data)
	}
}

func TestNotifyToolMissingMessageIsError(t *testing.T) {
	m, id := newTestWorkspace(t)
	s, _ := NewServer(m, id, nil)
	cs := connect(t, s)

	// The SDK schema requires `message`; an omitted field is rejected before
	// the handler runs (which would also error). Either way it is an error.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "notify",
		Arguments: map[string]any{"id": id},
	})
	if err == nil && (res == nil || !res.IsError) {
		t.Error("expected an error when message is missing")
	}
}

// TestNotifyRejectsTraversal is a direct unit test of the validation that
// guards against an arbitrary-file-write via a crafted id.
func TestNotifyRejectsTraversal(t *testing.T) {
	m, _ := newTestWorkspace(t)
	if _, err := notifyWorkspace(m, "../../etc/cron.d/x", "evil"); err == nil {
		t.Error("expected a path-traversal id to be rejected")
	}
	if _, err := notifyWorkspace(m, "unregistered-id", "hi"); err == nil {
		t.Error("expected an unregistered id to be rejected")
	}
}

func TestValidBranchNameRejectsBadNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if err := validBranchName(context.Background(), dir, "feature/ok"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	for _, bad := range []string{"bad branch", "with..dots", "-leading", "tail.lock"} {
		if err := validBranchName(context.Background(), dir, bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}
