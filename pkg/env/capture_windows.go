//go:build windows
// +build windows

// Package env captures the user's interactive login-shell environment so
// spawned agents see the same PATH and variables a terminal session would.
// Windows implementation uses simpler environment capture.
package env

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ShellEnv holds the captured user shell environment.
type ShellEnv struct {
	Path       string
	ClaudePath string
	CodexPath  string
	AllVars    map[string]string
}

// Captured returns the current environment on Windows.
var Captured = sync.OnceValue(func() *ShellEnv {
	cur := envMapFromCurrent()
	path := cur["PATH"]
	return &ShellEnv{
		Path:       path,
		ClaudePath: lookPathIn("claude", path),
		CodexPath:  lookPathIn("codex", path),
		AllVars:    cur,
	}
})

// lookPathIn resolves an executable name against the given PATH string.
func lookPathIn(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
	}
	return ""
}

// LookPath resolves an executable name against the captured PATH.
func LookPath(name string) string {
	return lookPathIn(name, Captured().Path)
}

// Environ returns the process environment.
func Environ() []string {
	merged := envMapFromCurrent()
	if c := Captured(); c != nil {
		for k, v := range c.AllVars {
			merged[k] = v
		}
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func envMapFromCurrent() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}
