---
title: "Verify OS process identity (PGID + start token) before signaling a persisted PID"
date: 2026-06-07
category: design-patterns
module: orkestra-agent-runtime
problem_type: design_pattern
component: assistant
severity: high
applies_when:
  - "A long-lived child process is tracked across separate OS processes (its PID is persisted to disk and signaled later by a different command)"
  - "A stop/cleanup path signals a process it did not directly start and still holds a handle to"
  - "Running with broad signaling authority (process-group kills, bypassed permissions) where hitting the wrong target is destructive"
root_cause: logic_error
resolution_type: code_fix
related_components:
  - tooling
  - testing-framework
tags:
  - process-management
  - pid-recycling
  - signal-safety
  - cross-process-state
  - posix
  - go
---

# Verify OS process identity before signaling a persisted PID

## Context

Orkestra runs `run` and `stop` as separate, short-lived OS processes. An in-memory `*exec.Cmd` handle cannot be shared between them, so `run` persists the agent's process identifiers to `sessions.json` and `stop` reads them back later to terminate the agent.

The friction is that **a bare PID is not a stable identifier**. The kernel recycles PIDs: after the agent exits, its PID number is free to be reassigned to an unrelated process. If `stop` (or `IsRunning`) trusts only the persisted PID, it can signal a completely different process that happened to inherit the number. Under `bypassPermissions` mode — where Orkestra signals process groups — sending `SIGTERM`/`SIGKILL` to the wrong target is destructive.

A naive fix does not close the gap (session history):

- **PGID alone is insufficient.** `syscall.Getpgid(pid)` can succeed and even match in degenerate cases; a recycled leader PID cannot be distinguished from the original by group membership alone. The start-token gap here was independently flagged by the adversarial, correctness, and reliability reviewers in the prior review pass. (session history)
- **A wall-clock timestamp is insufficient.** `time.Now()` records when Orkestra *wrote* the record, not a property of the OS process, so it cannot tell the original process apart from a recycled one. The first implementation persisted exactly this and it was rejected on review. (session history)

## Guidance

Record an **OS-derived process-start token** at spawn time, persist the triple `{pid, pgid, startedAt}`, and verify all three before any signal.

1. Start the child as its own process-group leader so the recorded PGID is meaningful (`pkg/process/process.go`):

```go
func SysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
```

2. Capture the start token immediately after `cmd.Start()`. The token is platform-specific and **comparison-only** — not a wall-clock value (`pkg/process/process.go:51-103`):

```go
func StartedAt(pid int) (int64, error) {
	switch runtime.GOOS {
	case "linux":
		return linuxStartTicks(pid)   // field 22 (starttime) of /proc/<pid>/stat
	case "darwin":
		return darwinStartUnixNano(pid) // `ps -o lstart=` parsed to nanoseconds
	default:
		return 0, fmt.Errorf("process identity unsupported on %s", runtime.GOOS)
	}
}
```

   `linuxStartTicks` trims the parenthesized `comm` field before splitting, so a process name containing spaces or parentheses cannot misalign the field index. `darwinStartUnixNano` bounds its `ps` call with a one-second context timeout.

3. Persist the triple right after start, and **abort the run if the token cannot be captured** (`pkg/runner/claude_runner.go`):

```go
pid := cmd.Process.Pid
startedAt, err := process.StartedAt(pid)
if err != nil {
	_ = process.TerminateGroup(pid, process.DefaultGrace)
	_ = cmd.Wait()
	return nil, fmt.Errorf("failed to capture agent process identity: %w", err)
}
if err := r.workspaceManager.SetSessionProcess(workspaceID, string(agent), pid, pid, startedAt); err != nil {
	_ = process.TerminateGroup(pid, process.DefaultGrace)
	_ = cmd.Wait()
	return nil, fmt.Errorf("failed to persist agent process identity: %w", err)
}
```

   The persisted fields are `PID int`, `PGID int`, `StartedAt int64` (`pkg/workspace/workspace.go`).

4. Before signaling, require that the live PGID of the PID equals the recorded PGID **and** the current start token equals the recorded one (`pkg/process/process.go:107-120`):

```go
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
```

5. Wire the check into both the read path and the signal path. `IsRunning` reads false unless the same recorded process is live, and `Stop` clears stale state instead of signaling (`pkg/runner/claude_runner.go:157-190`):

```go
func (r *Runner) IsRunning(workspaceID string) bool {
	s, err := r.workspaceManager.GetSession(workspaceID)
	if err != nil {
		return false
	}
	return process.IdentityMatches(s.PID, s.PGID, s.StartedAt)
}

// Stop:
if s.PID > 0 || s.PGID > 0 {
	if !process.IdentityMatches(s.PID, s.PGID, s.StartedAt) {
		// recycled or absent: clear state, do NOT signal anything
		_ = r.workspaceManager.ClearSessionProcess(workspaceID)
		_ = r.workspaceManager.UpdateWorkspaceStatus(workspaceID, "inactive")
		return nil
	}
	if err := process.TerminateGroup(s.PGID, process.DefaultGrace); err != nil {
		return fmt.Errorf("failed to terminate agent for workspace %s: %w", workspaceID, err)
	}
}
```

## Why This Matters

Without identity verification, a `stop` issued after the agent has already exited can signal an innocent process that inherited the recycled PID. The damage is amplified by process-group signaling and bypassed permissions. The start token is the only one of the three checks that is a kernel-derived property of the *specific process instance*; PGID equality plus start-token equality together are what make a recycled PID detectable. This is a cross-process-state problem that does not arise when a parent keeps the live `*exec.Cmd` handle — it is specific to designs that persist a process handle to disk and act on it from a different command.

## When to Apply

- Any long-lived child process whose identifier is persisted and later signaled by a separate process.
- Daemon/supervisor designs where start and stop are distinct invocations.
- Any code path that kills by stored PID/PGID rather than an owned, live handle.

## Examples

Before — bare PID and a wall-clock timestamp, signaling on PGID presence alone (session history):

```go
// IsRunning: trusts a bare PID
return s.PID > 0 && process.Alive(s.PID)

// run: persists a wall-clock timestamp, not an OS token
SetSessionProcess(workspaceID, string(agent), pid, pid, time.Now().UnixNano())

// Stop: signals on PGID presence alone, no identity check
if s.PGID > 0 {
	process.TerminateGroup(s.PGID, process.DefaultGrace)
}
```

After — OS-derived token, persisted triple, three-way verification before any signal (see Guidance for `IdentityMatches`, `IsRunning`, and `Stop`).

## Prevention

Tests that lock the behavior in:

- `pkg/process/process_test.go:58` `TestIdentityMatchesProcessGroupLeader` — a live leader matches its own `{pid, pgid, startedAt}`; a mismatched PGID (`pid+1`) or mismatched token (`startedAt+1`) both fail. Directly exercises recycled-PID rejection.
- `pkg/runner/claude_runner_test.go:203` `TestStopClearsStaleProcessIdentity` — a session whose `{PID, PGID, StartedAt}` cannot match any live process is cleared by `Stop` without signaling.

Rule of thumb: never signal a process you only know by a persisted integer. Persist an OS-derived identity token alongside it and verify before sending any signal.

## Related

- Incident record this pattern was extracted from: [Real Binary Agent Process Hardening](../integration-issues/real-binary-agent-process-hardening.md)
- Sibling pattern for bounding a child's whole subtree on timeout: [Kill the whole process group on timeout, not just the direct child](./kill-process-group-on-timeout.md)
- `CONCEPTS.md` → **Process Identity**
