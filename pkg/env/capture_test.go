package env

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseEnvBlockExtractsBetweenMarkers(t *testing.T) {
	out := "Now using node v20\n" + marker + "\nPATH=/usr/bin\nFOO=bar\n_=ignored\nSHLVL=2\n" + marker + "\ntrailing noise\n"
	vars := parseEnvBlock(out)

	if vars["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %q, want /usr/bin", vars["PATH"])
	}
	if vars["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", vars["FOO"])
	}
	// rc-file noise outside the markers must not leak in.
	if _, ok := vars["Now using node v20"]; ok {
		t.Error("rc-file banner leaked into parsed env")
	}
	// Shell-internal keys are excluded.
	if _, ok := vars["_"]; ok {
		t.Error("internal key _ should be excluded")
	}
	if _, ok := vars["SHLVL"]; ok {
		t.Error("internal key SHLVL should be excluded")
	}
}

func TestParseEnvBlockHandlesValueWithEquals(t *testing.T) {
	out := marker + "\nDSN=user=admin;pass=x\n" + marker
	vars := parseEnvBlock(out)
	if vars["DSN"] != "user=admin;pass=x" {
		t.Errorf("DSN = %q, want the full value after the first =", vars["DSN"])
	}
}

func TestLookPathIn(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-executable file with the codex name must not be resolved.
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := dir + string(os.PathListSeparator) + filepath.Join(dir, "missing")
	if got := lookPathIn("claude", path); got != bin {
		t.Errorf("lookPathIn(claude) = %q, want %q", got, bin)
	}
	if got := lookPathIn("codex", path); got != "" {
		t.Errorf("lookPathIn(codex) = %q, want empty (not executable)", got)
	}
	if got := lookPathIn("nope", path); got != "" {
		t.Errorf("lookPathIn(nope) = %q, want empty", got)
	}
}

func TestCaptureFromShellHappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell capture")
	}
	sh, err := lookShell(t)
	if err != "" {
		t.Skip(err)
	}
	vars, ok := captureFromShell(sh, captureTimeout)
	if !ok {
		t.Fatal("expected capture to succeed for a real shell")
	}
	if vars["PATH"] == "" {
		t.Error("captured env should contain PATH")
	}
}

func TestCaptureFromShellTimeoutFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell capture")
	}
	sh, err := lookShell(t)
	if err != "" {
		t.Skip(err)
	}
	// A 1ns deadline cannot complete a shell spawn, so capture must report
	// failure (ok=false) rather than hang — the caller then uses the fallback.
	start := time.Now()
	if _, ok := captureFromShell(sh, 1); ok {
		t.Error("expected capture to fail under an impossibly short deadline")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("capture should not hang well past its deadline")
	}
}

// lookShell returns a usable POSIX shell path, or a skip reason.
func lookShell(t *testing.T) (string, string) {
	t.Helper()
	for _, sh := range []string{"/bin/sh", "/bin/zsh", "/bin/bash"} {
		if fi, err := os.Stat(sh); err == nil && !fi.IsDir() {
			return sh, ""
		}
	}
	return "", "no POSIX shell available"
}
