package gitauth

import (
	"context"
	"os/exec"
	"slices"
	"testing"
)

func TestResolveTokenSuccess(t *testing.T) {
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "printf", "tok-123")
	}
	defer func() { execCommandContext = exec.CommandContext }()

	got, err := ResolveToken("my-profile")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tok-123" {
		t.Errorf("token = %q, want tok-123", got)
	}
}

func TestResolveTokenError(t *testing.T) {
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "false")
	}
	defer func() { execCommandContext = exec.CommandContext }()

	if _, err := ResolveToken("missing"); err == nil {
		t.Error("expected error when gh exits non-zero")
	}
}

func TestResolveTokenContextCanceled(t *testing.T) {
	execCommandContext = exec.CommandContext
	defer func() { execCommandContext = exec.CommandContext }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled: the command must not run
	if _, err := ResolveTokenContext(ctx, "any"); err == nil {
		t.Error("expected error when context is already canceled")
	}
}

func TestBuildEnvVars(t *testing.T) {
	got := BuildEnvVars("abc")
	if !slices.Contains(got, "GH_TOKEN=abc") {
		t.Errorf("BuildEnvVars = %v, want it to contain GH_TOKEN=abc", got)
	}
}
