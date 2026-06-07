package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/adryanev/orkestra/pkg/env"
	"github.com/adryanev/orkestra/pkg/workspace"
)

// TestSessionInfoOutputHasNoCredentials locks the run/resume output shape: the
// reported SessionInfo must never carry environment-derived credentials (R21).
// This guards against a future field that would serialize the injected token.
func TestSessionInfoOutputHasNoCredentials(t *testing.T) {
	si := SessionInfo{SessionID: "s", ThreadID: "t", Usage: UsageReport{InputTokens: 1, OutputTokens: 2, Model: "m"}}
	b, err := json.Marshal(si)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"GH_TOKEN", "PATH=", "ghp_", "VIRTUAL_ENV"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("session output JSON leaked %q: %s", banned, b)
		}
	}
}

func envValue(vars []string, key string) (string, bool) {
	for _, kv := range vars {
		if i := strings.IndexByte(kv, '='); i > 0 && kv[:i] == key {
			return kv[i+1:], true
		}
	}
	return "", false
}

func TestComposeEnvOverlaysAndDedups(t *testing.T) {
	shell := &env.ShellEnv{AllVars: map[string]string{
		"PATH":       "/shell/bin",
		"SHELL_ONLY": "yes",
	}}
	vars := composeEnv(shell, "tok-9")

	// Token injected.
	if v, ok := envValue(vars, "GH_TOKEN"); !ok || v != "tok-9" {
		t.Errorf("GH_TOKEN = %q,%v want tok-9", v, ok)
	}
	// Shell var present.
	if v, _ := envValue(vars, "SHELL_ONLY"); v != "yes" {
		t.Errorf("SHELL_ONLY = %q, want yes", v)
	}
	// PATH from shell overrides the process PATH; only one PATH entry exists.
	count := 0
	for _, kv := range vars {
		if strings.HasPrefix(kv, "PATH=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one PATH entry, got %d", count)
	}
	if v, _ := envValue(vars, "PATH"); v != "/shell/bin" {
		t.Errorf("PATH = %q, want /shell/bin (shell overrides process)", v)
	}
}

func TestComposeEnvNoToken(t *testing.T) {
	vars := composeEnv(&env.ShellEnv{AllVars: map[string]string{}}, "")
	if _, ok := envValue(vars, "GH_TOKEN"); ok {
		t.Error("GH_TOKEN should be absent when no token provided")
	}
}

func TestResolveBinary(t *testing.T) {
	shell := &env.ShellEnv{ClaudePath: "/abs/claude", CodexPath: "/abs/codex"}
	if got := resolveBinary(Claude, shell); got != "/abs/claude" {
		t.Errorf("claude = %q, want /abs/claude", got)
	}
	if got := resolveBinary(Codex, shell); got != "/abs/codex" {
		t.Errorf("codex = %q, want /abs/codex", got)
	}
	// Fallback to bare name when not resolved.
	if got := resolveBinary(Claude, &env.ShellEnv{}); got != "claude" {
		t.Errorf("fallback = %q, want claude", got)
	}
}

func TestBuildAgentArgsClaudeFresh(t *testing.T) {
	name, args, err := buildAgentArgs(Claude, "do it", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "claude" {
		t.Errorf("name = %q, want claude", name)
	}
	if slices.Contains(args, "--resume") {
		t.Error("fresh run should not contain --resume")
	}
	if !slices.Contains(args, "bypassPermissions") {
		t.Error("missing permission mode")
	}
	// The LSP tool is disabled so orkestra's LSP tools are used instead.
	i := slices.Index(args, "--disallowedTools")
	if i < 0 || i+1 >= len(args) || !strings.Contains(args[i+1], "LSP") {
		t.Errorf("expected LSP in --disallowedTools, got %v", args)
	}
	// No MCP config path was supplied, so --mcp-config must be absent.
	if slices.Contains(args, "--mcp-config") {
		t.Error("--mcp-config should be absent when no path is given")
	}
}

func TestBuildAgentArgsClaudeMCPConfig(t *testing.T) {
	_, args, err := buildAgentArgs(Claude, "go", "", "", "/cfg/ws.json", "")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--mcp-config")
	if i < 0 || i+1 >= len(args) || args[i+1] != "/cfg/ws.json" {
		t.Errorf("expected --mcp-config /cfg/ws.json in %v", args)
	}
}

func TestBuildAgentArgsClaudeResume(t *testing.T) {
	_, args, err := buildAgentArgs(Claude, "continue", "sess-123", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || i+1 >= len(args) || args[i+1] != "sess-123" {
		t.Errorf("expected --resume sess-123 in %v", args)
	}
}

func TestBuildAgentArgsCodexResume(t *testing.T) {
	_, args, err := buildAgentArgs(Codex, "continue", "", "thread-abc", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "resume", "--json", "--dangerously-bypass-approvals-and-sandbox", "thread-abc", "continue"}
	if !slices.Equal(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildAgentArgsCodexFresh(t *testing.T) {
	_, args, err := buildAgentArgs(Codex, "go", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "exec" || slices.Contains(args, "resume") {
		t.Errorf("fresh codex run should be `exec ... go`, got %v", args)
	}
	if args[len(args)-1] != "go" {
		t.Errorf("prompt should be last arg, got %v", args)
	}
}

func TestBuildAgentArgsUnsupported(t *testing.T) {
	if _, _, err := buildAgentArgs(AgentType("gemini"), "x", "", "", "", ""); err == nil {
		t.Error("expected error for unsupported agent")
	}
}

func testRunnerWithWorkspace(t *testing.T) (*Runner, *workspace.Manager, string) {
	t.Helper()
	cfg := t.TempDir()
	worktree := filepath.Join(cfg, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	m, id := registerWorkspace(t, cfg, worktree)
	return NewRunner(m), m, id
}

func TestRunResumeRejectsMismatchedSavedAgent(t *testing.T) {
	r, m, id := testRunnerWithWorkspace(t)
	if err := m.AddSession(workspace.Session{WorkspaceID: id, Agent: string(Codex), ThreadID: "thread-abc"}); err != nil {
		t.Fatal(err)
	}

	_, err := r.Run(id, Claude, "continue", true, false, false, "")
	if err == nil || !strings.Contains(err.Error(), "saved session is for codex") {
		t.Fatalf("expected wrong-agent resume error, got %v", err)
	}
}

func TestRunResumeRequiresAgentSpecificID(t *testing.T) {
	r, m, id := testRunnerWithWorkspace(t)
	if err := m.AddSession(workspace.Session{WorkspaceID: id, Agent: string(Codex), SessionID: "sid-only"}); err != nil {
		t.Fatal(err)
	}

	_, err := r.Run(id, Codex, "continue", true, false, false, "")
	if err == nil || !strings.Contains(err.Error(), "no Codex thread_id") {
		t.Fatalf("expected missing thread id error, got %v", err)
	}
}

func TestStopClearsStaleProcessIdentity(t *testing.T) {
	r, m, id := testRunnerWithWorkspace(t)
	if err := m.AddSession(workspace.Session{
		WorkspaceID: id,
		Agent:       string(Claude),
		SessionID:   "sid",
		PID:         999999,
		PGID:        999999,
		StartedAt:   1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateWorkspaceStatus(id, "active"); err != nil {
		t.Fatal(err)
	}

	if err := r.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	s, err := m.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if s.PID != 0 || s.PGID != 0 || s.StartedAt != 0 {
		t.Fatalf("process state should be cleared, got %+v", s)
	}
	ws, err := m.GetWorkspace(id)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Status != "inactive" {
		t.Fatalf("workspace status = %q, want inactive", ws.Status)
	}
}

func TestCaptureClaudeSessionAndUsage(t *testing.T) {
	var si SessionInfo
	captureClaude(`{"type":"system","session_id":"sid-1"}`, &si)
	if si.SessionID != "sid-1" {
		t.Errorf("session id = %q, want sid-1", si.SessionID)
	}
	// A later system line must not overwrite the first session id.
	captureClaude(`{"type":"system","session_id":"sid-2"}`, &si)
	if si.SessionID != "sid-1" {
		t.Errorf("session id overwritten to %q", si.SessionID)
	}

	captureClaude(`{"type":"result","model":"opus","usage":{"input_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":2,"output_tokens":7}}`, &si)
	if si.Usage.InputTokens != 17 {
		t.Errorf("input tokens = %d, want 17 (10+5+2)", si.Usage.InputTokens)
	}
	if si.Usage.OutputTokens != 7 {
		t.Errorf("output tokens = %d, want 7", si.Usage.OutputTokens)
	}
	if si.Usage.Model != "opus" {
		t.Errorf("model = %q, want opus", si.Usage.Model)
	}
}

func TestCaptureClaudeAssistantText(t *testing.T) {
	var si SessionInfo
	got := captureClaude(`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"},{"type":"tool_use"},{"type":"text","text":" world"}]}}`, &si)
	if got != "hello world\n" {
		t.Errorf("assistant text = %q, want %q", got, "hello world\n")
	}
}

func TestCaptureClaudeMalformedLine(t *testing.T) {
	var si SessionInfo
	if got := captureClaude("not json", &si); got != "" {
		t.Errorf("malformed line should yield empty display, got %q", got)
	}
}

func TestCaptureCodex(t *testing.T) {
	var si SessionInfo
	captureCodex(map[string]interface{}{"type": "thread.started", "thread_id": "th-9"}, &si)
	if si.ThreadID != "th-9" {
		t.Errorf("thread id = %q, want th-9", si.ThreadID)
	}
	got := captureCodex(map[string]interface{}{
		"type": "item.completed",
		"item": map[string]interface{}{"type": "agent_message", "text": "done"},
	}, &si)
	if got != "done\n" {
		t.Errorf("agent message = %q, want %q", got, "done\n")
	}
	captureCodex(map[string]interface{}{
		"type":  "turn.completed",
		"usage": map[string]interface{}{"input_tokens": float64(3), "output_tokens": float64(4)},
	}, &si)
	if si.Usage.InputTokens != 3 || si.Usage.OutputTokens != 4 {
		t.Errorf("codex usage = %+v, want input 3 output 4", si.Usage)
	}
}
