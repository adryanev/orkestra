//go:build unix
// +build unix

// Package process provides cross-process child-control primitives for orkestra.
//
// orkestra runs `run` and `stop` as separate, short-lived OS processes, so an
// in-memory *exec.Cmd handle cannot be shared between them. Instead `run`
// persists the agent's PID and process-group id (PGID) to sessions.json, and
// `stop` reads them back and signals the group. Placing the child in its own
// process group (Setpgid) lets `stop` terminate the agent and every descendant
// with a single group signal, and gives recycled-PID safety: once the agent's
// group exits, the kernel has no process group with that id, so a later
// unrelated process that happens to reuse the bare PID is never signaled.
//
// These helpers are POSIX-specific (process groups, signals). orkestra targets
// Linux and macOS; a Windows port would need separate handling.
package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultGrace is the time a process group is given to exit after SIGTERM
// before SIGKILL is sent.
const DefaultGrace = 5 * time.Second

// SysProcAttr returns the attributes that start a child as the leader of its
// own process group, so the whole group can be signaled later by a separate
// process. The new group's id equals the child's PID.
func SysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// PGID returns the current process-group id for pid.
func PGID(pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	return syscall.Getpgid(pid)
}

// Alive reports whether a process with pid currently exists. Signal 0 performs
// existence/permission checking without delivering a signal; EPERM means the
// process exists but is owned by another user, which still counts as alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// StartedAt returns an OS-derived process-start token for pid. The value is
// platform-specific and only meant to be compared with a later StartedAt call
// for the same pid; it is not necessarily a wall-clock timestamp.
func StartedAt(pid int) (int64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	switch runtime.GOOS {
	case "linux":
		return linuxStartTicks(pid)
	case "darwin":
		return darwinStartUnixNano(pid)
	default:
		return 0, fmt.Errorf("process identity unsupported on %s", runtime.GOOS)
	}
}

func linuxStartTicks(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	stat := string(data)
	endComm := strings.LastIndexByte(stat, ')')
	if endComm == -1 || endComm+2 >= len(stat) {
		return 0, fmt.Errorf("malformed /proc stat for pid %d", pid)
	}
	fields := strings.Fields(stat[endComm+2:])
	// /proc/<pid>/stat field 22 is starttime. After trimming the comm field,
	// fields[0] is field 3 (state), so starttime lands at index 19.
	if len(fields) <= 19 {
		return 0, fmt.Errorf("missing starttime in /proc stat for pid %d", pid)
	}
	return strconv.ParseInt(fields[19], 10, 64)
}

func darwinStartUnixNano(pid int) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0, fmt.Errorf("missing start time for pid %d", pid)
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", text, time.Local)
	if err != nil {
		return 0, err
	}
	return t.UnixNano(), nil
}

// IdentityMatches verifies that pid is still the process-group leader that was
// recorded at spawn time. This protects `stop` from signaling a recycled PID.
func IdentityMatches(pid, pgid int, startedAt int64) bool {
	if pid <= 0 || pgid <= 0 || startedAt <= 0 {
		return false
	}
	currentPGID, err := syscall.Getpgid(pid)
	if err != nil || currentPGID != pgid {
		return false
	}
	currentStartedAt, err := StartedAt(pid)
	if err != nil {
		return false
	}
	return currentStartedAt == startedAt
}

// groupAlive reports whether any process remains in the process group pgid.
// Signaling the negative group id with signal 0 returns ESRCH only when the
// whole group is gone, so this detects a surviving child even after the group
// leader has exited.
func groupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// GroupID returns the process group ID of pid as reported by the kernel.
// Use this immediately after cmd.Start() to capture the real pgid rather than
// assuming pgid == pid (even when Setpgid is set).
func GroupID(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

// TerminateGroup terminates the process group led by pgid. It sends SIGTERM,
// waits up to grace for the group leader to exit, then sends SIGKILL if the
// leader is still alive. A group that no longer exists (ESRCH) is treated as
// success, so stop is idempotent against an already-exited agent.
func TerminateGroup(pgid int, grace time.Duration) error {
	if pgid <= 0 {
		return nil
	}
	// Negative pid targets the whole process group.
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // group already gone
		}
		return err
	}

	// Poll the whole group, not just the leader: a child that outlives the
	// leader must still force the SIGKILL escalation.
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !groupAlive(pgid) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
