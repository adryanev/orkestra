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
	"errors"
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

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !Alive(pgid) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
