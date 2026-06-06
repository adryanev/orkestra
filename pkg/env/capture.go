package env

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ShellEnv holds captured user shell environment.
type ShellEnv struct {
	Path       string
	ClaudePath string
	CodexPath  string
	AllVars    map[string]string
}

// Captured returns cached shell environment from an interactive login shell.
// Uses sync.OnceValue for one-time lazy init (Go 1.21+).
// This ensures spawned agent processes get the same PATH and env vars
// as a regular terminal session (nvm/fnm paths, GOPATH, etc.).
var Captured = sync.OnceValue(func() *ShellEnv {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	// Run login shell to capture full env
	cmd := exec.Command(shell, "-lic", "/usr/bin/env")
	out, err := cmd.Output()
	if err != nil {
		// Fallback: capture current env
		return &ShellEnv{
			AllVars: envMapFromCurrent(),
		}
	}

	allVars := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.IndexByte(line, '='); idx > 0 {
			key := line[:idx]
			val := line[idx+1:]
			// Skip shell internals
			if strings.HasPrefix(key, "_") || key == "SHLVL" || key == "ZSH_EVAL_CONTEXT" {
				continue
			}
			allVars[key] = val
		}
	}

	// Resolve claude binary path
	claudeOut, _ := exec.Command(shell, "-lic", "command -v claude").Output()
	claudePath := strings.TrimSpace(string(claudeOut))

	// Resolve codex binary path
	codexOut, _ := exec.Command(shell, "-lic", "command -v codex").Output()
	codexPath := strings.TrimSpace(string(codexOut))

	return &ShellEnv{
		Path:       allVars["PATH"],
		ClaudePath: claudePath,
		CodexPath:  codexPath,
		AllVars:    allVars,
	}
})

func envMapFromCurrent() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}