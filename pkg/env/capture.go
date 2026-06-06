// Package env captures the user's interactive login-shell environment so
// spawned agents see the same PATH and variables a terminal session would
// (nvm/fnm/asdf PATH entries, GOPATH, etc.).
//
// Filtering policy: the full captured environment is forwarded to the agent by
// design — orkestra runs trusted agents and the purpose of the capture is
// terminal parity. Only shell-internal bookkeeping keys are dropped. Secrets
// that would reach the agent are no different from a terminal session; the
// separate concern of credentials reaching an *output sink* (logs, streaming
// stdout, --json) is handled where output is produced, not here.
package env

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// captureTimeout bounds each shell invocation. An interactive rc file that
// hangs (waiting on input, a slow network mount) must not stall orkestra, so
// the capture fails closed to the current environment after this deadline.
const captureTimeout = 5 * time.Second

// marker delimits the real `env` output from rc-file chatter (motd, version
// manager banners) that interactive shells print around it.
const marker = "__ORKESTRA_ENV__"

// ShellEnv holds the captured user shell environment.
type ShellEnv struct {
	Path       string
	ClaudePath string
	CodexPath  string
	AllVars    map[string]string
}

// Captured returns the cached shell environment from an interactive login
// shell. sync.OnceValue makes the (relatively expensive) shell spawn happen at
// most once per process.
var Captured = sync.OnceValue(func() *ShellEnv {
	vars, ok := captureFromShell(shellPath(), captureTimeout)
	if !ok {
		// Fail closed to the current environment so the agent still launches.
		cur := envMapFromCurrent()
		return &ShellEnv{Path: cur["PATH"], AllVars: cur}
	}
	path := vars["PATH"]
	return &ShellEnv{
		Path:       path,
		ClaudePath: lookPathIn("claude", path),
		CodexPath:  lookPathIn("codex", path),
		AllVars:    vars,
	}
})

// shellPath returns the user's shell, defaulting to zsh when $SHELL is unset.
func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// captureFromShell runs an interactive login shell that prints `env` wrapped in
// delimiter markers, under a deadline. It returns the parsed variables, or
// ok=false on timeout or error so the caller can fall back.
func captureFromShell(shell string, timeout time.Duration) (map[string]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// echo MARKER; env; echo MARKER  — the markers let us discard rc-file noise
	// printed before or after the real output.
	script := "echo " + marker + "; /usr/bin/env; echo " + marker
	cmd := exec.CommandContext(ctx, shell, shellArgs(shell, script)...)
	// Discard rc-file chatter written to stderr (banners, warnings).
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	vars := parseEnvBlock(string(out))
	if len(vars) == 0 {
		return nil, false
	}
	return vars, true
}

// shellArgs returns the login+interactive invocation for the given shell. fish
// uses long flags; POSIX shells accept the combined -lic form.
func shellArgs(shell, script string) []string {
	if filepath.Base(shell) == "fish" {
		return []string{"--login", "--interactive", "-c", script}
	}
	return []string{"-lic", script}
}

// parseEnvBlock extracts the `key=value` lines between the two markers and
// returns them as a map, excluding shell-internal bookkeeping keys. When the
// markers are absent (unexpected) it parses the whole output defensively.
func parseEnvBlock(out string) map[string]string {
	lines := strings.Split(out, "\n")

	// Narrow to the slice between the first and last marker when present.
	start, end := -1, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			if start == -1 {
				start = i
			} else {
				end = i
			}
		}
	}
	if start != -1 && end != -1 && end > start {
		lines = lines[start+1 : end]
	}

	vars := make(map[string]string)
	for _, line := range lines {
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := line[:idx]
		if isShellInternal(key) {
			continue
		}
		vars[key] = line[idx+1:]
	}
	return vars
}

// isShellInternal reports whether key is shell bookkeeping that should not be
// propagated to the agent (it would either be meaningless or override the
// agent's own value).
func isShellInternal(key string) bool {
	switch key {
	case "_", "SHLVL", "ZSH_EVAL_CONTEXT", "OLDPWD":
		return true
	}
	return false
}

// lookPathIn resolves an executable name against the given PATH string without
// spawning a shell (the captured PATH already reflects the user's environment).
// It returns an absolute path, or "" when not found.
func lookPathIn(name, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

// LookPath resolves an executable name against the captured login-shell PATH,
// so a server installed via nvm/fnm/asdf resolves the same way it would in a
// terminal. It returns an absolute path, or "" when not found.
func LookPath(name string) string {
	return lookPathIn(name, Captured().Path)
}

// Environ returns the process environment overlaid with the captured
// login-shell variables (last wins), suitable for child processes that need
// terminal parity, such as language servers.
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
