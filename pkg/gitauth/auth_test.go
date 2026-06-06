package gitauth

import (
	"os/exec"
	"slices"
	"testing"
)

func TestResolveTokenSuccess(t *testing.T) {
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("printf", "tok-123")
	}
	defer func() { execCommand = exec.Command }()

	got, err := ResolveToken("my-profile")
	if err != nil {
		t.Fatal(err)
	}
	if got != "tok-123" {
		t.Errorf("token = %q, want tok-123", got)
	}
}

func TestResolveTokenError(t *testing.T) {
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}
	defer func() { execCommand = exec.Command }()

	if _, err := ResolveToken("missing"); err == nil {
		t.Error("expected error when gh exits non-zero")
	}
}

func TestBuildEnvVars(t *testing.T) {
	got := BuildEnvVars("abc")
	if !slices.Contains(got, "GH_TOKEN=abc") {
		t.Errorf("BuildEnvVars = %v, want it to contain GH_TOKEN=abc", got)
	}
}
