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
