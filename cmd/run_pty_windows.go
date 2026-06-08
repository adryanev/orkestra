//go:build windows

package cmd

import (
	"fmt"

	"github.com/adryanev/orkestra/pkg/runner"
)

// runPTYMode is a no-op stub on Windows since PTY operations are not supported.
func runPTYMode(workspaceID string, agent runner.AgentType, prompt, model, effort string) {
	emitError(fmt.Errorf("PTY mode is not supported on Windows"))
}
