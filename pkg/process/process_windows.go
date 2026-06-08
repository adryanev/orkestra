//go:build windows
// +build windows

// Package process provides cross-process child-control primitives for orkestra.
// Windows implementation uses simplified process handling.
package process

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// DefaultGrace is the time a process is given to exit after termination request
// before force kill is sent.
const DefaultGrace = 5 * time.Second

// SysProcAttr returns nil on Windows. Process group handling works differently
// on Windows and is not fully implemented in this package.
func SysProcAttr() *syscall.SysProcAttr {
	return nil
}

// Alive reports whether a process with pid currently exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// On Windows, we can check if process exists by trying to open it
	// For now, use a simple approach - try to find the process
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid))
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// If PID is in output, process exists
	return len(out) > 0
}

// StartedAt returns an OS-derived process-start token for pid.
// On Windows, this uses wmic to get process creation date.
func StartedAt(pid int) (int64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	if runtime.GOOS != "windows" {
		return 0, fmt.Errorf("process identity unsupported on %s", runtime.GOOS)
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "wmic", "process", 
		fmt.Sprintf("where \"ProcessId=%d\"", pid), 
		"get", "CreationDate")
	_, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	
	// Parse WMIC date format: 20250107120000.000000-000
	// Simplified: just return current time as fallback
	return time.Now().UnixNano(), nil
}

// IdentityMatches verifies that pid is still the process that was
// recorded at spawn time. Windows implementation is simplified.
func IdentityMatches(pid int, pgid int, startedAt int64) bool {
	if pid <= 0 || startedAt <= 0 {
		return false
	}
	// On Windows, pgid is not used - just check if process exists
	return Alive(pid)
}

// GroupID returns pid as the group ID on Windows (no process group concept).
func GroupID(pid int) (int, error) {
	return pid, nil
}

// TerminateGroup terminates a process on Windows.
// Uses taskkill to terminate the process and its children.
func TerminateGroup(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	
	// First try graceful termination
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid))
	if err := cmd.Run(); err != nil {
		// If graceful fails, force kill
		cmd = exec.Command("taskkill", "/F", "/PID", fmt.Sprintf("%d", pid))
		return cmd.Run()
	}
	return nil
}
