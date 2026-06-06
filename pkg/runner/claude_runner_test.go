package runner

import (
	"slices"
	"testing"
)

func TestBuildAgentArgsClaudeFresh(t *testing.T) {
	name, args, err := buildAgentArgs(Claude, "do it", "", "")
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
}

func TestBuildAgentArgsClaudeResume(t *testing.T) {
	_, args, err := buildAgentArgs(Claude, "continue", "sess-123", "")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--resume")
	if i < 0 || i+1 >= len(args) || args[i+1] != "sess-123" {
		t.Errorf("expected --resume sess-123 in %v", args)
	}
}

func TestBuildAgentArgsCodexResume(t *testing.T) {
	_, args, err := buildAgentArgs(Codex, "continue", "", "thread-abc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "resume", "thread-abc", "--json", "continue"}
	if !slices.Equal(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildAgentArgsCodexFresh(t *testing.T) {
	_, args, err := buildAgentArgs(Codex, "go", "", "")
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
	if _, _, err := buildAgentArgs(AgentType("gemini"), "x", "", ""); err == nil {
		t.Error("expected error for unsupported agent")
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
