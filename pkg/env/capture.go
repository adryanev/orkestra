package env

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type ShellEnv struct {
	Path       string
	ClaudePath string
	CodexPath  string
	AllVars    map[string]string
}

// Captured returns cached shell environment from login shell.
// Uses sync.OnceValue for lazy initialization.
var Captured = sync.OnceValue(func() *ShellEnv {
	// Run: zsh -lic '/usr/bin/env'
	cmd := exec.Command("zsh", "-lic", "/usr/bin/env")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		// In a real application, this should be logged or handled more gracefully.
		panic(fmt.Sprintf("failed to create stdout pipe: %v", err))
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		panic(fmt.Sprintf("failed to start command: %v", err))
	}

	// Read the output line by line
	scanner := bufio.NewScanner(stdout)
	vars := make(map[string]string)
	var claudePath, codexPath string

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key, value := parts[0], parts[1]
			vars[key] = value
			switch key {
			case "PATH":
				vars[key] = value // Store the full PATH
			// We'll resolve claude and codex paths later if needed, or rely on PATH
			}
		}
	}

	if err := scanner.Err(); err != nil {
		panic(fmt.Sprintf("error reading stdout: %v", err))
	}

	// Wait for the command to finish
	if err := cmd.Wait(); err != nil {
		panic(fmt.Sprintf("command finished with error: %v", err))
	}

	// Resolve paths for 'claude' and 'codex' if they exist in PATH
	claudePath, _ = exec.LookPath("claude")
	codexPath, _ = exec.LookPath("codex")

	return &ShellEnv{
		Path:       vars["PATH"],
		ClaudePath: claudePath,
		CodexPath:  codexPath,
		AllVars:    vars,
	}
})
