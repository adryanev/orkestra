package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adryanev/orkestra/pkg/workspace"
)

// notifyWorkspace appends a timestamped message to the workspace's log file and
// returns the log path. The id is validated before any path is built: it must
// be a registered workspace and must contain no path-separator or ".." segment,
// so a crafted id ("../../.ssh/authorized_keys") cannot turn this logging tool
// into an arbitrary-file-write primitive (R20). It never writes to stdout — the
// MCP server owns stdout for the protocol stream (KTD8).
func notifyWorkspace(wm *workspace.Manager, id, message string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("missing 'id'")
	}
	if message == "" {
		return "", fmt.Errorf("missing 'message'")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid workspace id %q", id)
	}
	if _, err := wm.GetWorkspace(id); err != nil {
		return "", fmt.Errorf("unknown workspace %q", id)
	}

	logDir := filepath.Join(wm.ConfigDir(), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, id+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() { _ = f.Close() }()

	line := fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
	if _, err := f.WriteString(line); err != nil {
		return "", fmt.Errorf("failed to write notification: %w", err)
	}
	return logPath, nil
}
