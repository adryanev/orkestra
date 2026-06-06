---
title: "Bound a child command by killing the whole process group on timeout, not just the direct child"
date: 2026-06-07
category: design-patterns
module: orkestra-agent-runtime
problem_type: design_pattern
component: assistant
severity: medium
applies_when:
  - "An externally-bounded command may fork descendants (shells sourcing rc files, build tools, test runners)"
  - "A timeout must reclaim all work, not just the immediate child"
  - "Reading exec.Cmd fields from a cancellation goroutine while Start writes them (data-race risk)"
root_cause: async_timing
resolution_type: code_fix
related_components:
  - tooling
  - testing-framework
tags:
  - process-management
  - timeouts
  - os-exec
  - signals
  - data-race
  - go
---

# Kill the whole process group on timeout, not just the direct child

## Context

Orkestra captures the user's interactive login-shell environment so spawned agents inherit the same `PATH` (nvm/fnm/asdf, `GOPATH`). It does this by running the user's shell with `-lic "echo MARKER; /usr/bin/env; echo MARKER"` and parsing the output between the markers (`pkg/env/capture.go`).

The original implementation bounded that shell with `exec.CommandContext(ctx, ...)` plus `cmd.Output()`. This has two defects:

1. **Leaked descendants.** An interactive login shell sources rc files, which can fork their own subprocesses. `exec.CommandContext` sends a signal only to the *immediate* child it started. When the 5-second deadline (`captureTimeout`) fires, the shell is killed but any subprocess it spawned survives — leaking processes and the resources/ports they hold past the timeout. A backgrounded descendant that keeps the read pipe open can also block `cmd.Output()` indefinitely.
2. **A data race.** Bounding the command correctly requires reading the child's PID from a goroutine. Reading `cmd.Process` concurrently while `Start` writes it is a race caught by `go test -race`. (session history)

## Guidance

Start the child in its own process group, capture the immutable PID after `Start()`, read output through a pipe, and on cancellation kill the **negative PGID** so the whole group goes down. Use a `done` channel to stop the watcher when the command finishes normally (`pkg/env/capture.go:80-112`):

```go
cmd := exec.Command(shell, shellArgs(shell, script)...)
cmd.SysProcAttr = process.SysProcAttr() // Setpgid: true
cmd.Stderr = io.Discard
stdout, err := cmd.StdoutPipe()
if err != nil {
	return nil, false
}
if err := cmd.Start(); err != nil {
	return nil, false
}
pid := cmd.Process.Pid // capture once; never read cmd.Process from the goroutine

done := make(chan struct{})
go func() {
	select {
	case <-ctx.Done():
		_ = syscall.Kill(-pid, syscall.SIGKILL) // negative pid = whole group
	case <-done:
	}
}()
out, readErr := io.ReadAll(stdout)
waitErr := cmd.Wait()
close(done)
if readErr != nil || waitErr != nil {
	return nil, false
}
```

A negative argument to `syscall.Kill` targets the process group whose id equals that number, taking down the shell and every descendant it forked. The capture path uses an immediate `SIGKILL` because the deadline is a hard failure and the result is discarded.

For graceful termination elsewhere, `pkg/process` provides a sibling: `TerminateGroup` sends `SIGTERM` to `-pgid`, polls the group with `groupAlive` (which reports the group gone only when signaling it returns `ESRCH`), and escalates to `SIGKILL` on `-pgid` after a grace period (`DefaultGrace`, 5s):

```go
// pkg/process/process.go
func TerminateGroup(pgid int, grace time.Duration) error // SIGTERM -> poll -> SIGKILL on -pgid
func groupAlive(pgid int) bool                            // false only when -pgid signal returns ESRCH
```

## Why This Matters

Killing only the parent does not bound the work: leaked descendants outlive the timeout, holding resources, ports, and pipe file descriptors. A descendant holding the inherited stdout pipe can also wedge the output read forever, defeating the deadline entirely. The race-safety point is equally load-bearing — capturing the PID into a local after `Start()` is what makes the cancellation goroutine safe under `-race`.

## When to Apply

- Any externally-bounded command that may fork descendants — shells, build tools, test runners, package managers.
- Any place where a timeout must reclaim the entire subtree, not just the direct child.
- Any cancellation goroutine that needs the child PID — read it once into a local, never share `cmd.Process`.

## Examples

Before — `CommandContext` + `cmd.Output()`; kills only the direct child, leaks descendants:

```go
cmd := exec.CommandContext(ctx, shell, shellArgs(shell, script)...)
cmd.Stderr = nil
out, err := cmd.Output() // deadline cancels only this process; descendants survive
```

After — group leader plus negative-PGID kill on cancellation (see Guidance).

## Prevention

Tests that lock the behavior in (`pkg/env/capture_test.go`):

- `TestCaptureFromShellHappyPath` — real shell capture returns `PATH`.
- `TestCaptureFromShellTimeoutFallsBack` — a 1ns deadline yields `ok=false` and does not hang.
- `TestCaptureFromShellTimeoutKillsBackgroundDescendant` — the load-bearing test: a fake shell script `(sleep 5) & sleep 5` runs under a 50ms timeout and capture is asserted to return within 2s, proving the negative-PGID kill reaps the backgrounded descendant rather than waiting on it. This test would fail under the old `CommandContext` approach because the backgrounded child keeps the read pipe open.

Run the package with `go test -race ./pkg/env/...` to guard the data-race fix.

## Related

- Incident record this pattern was extracted from: [Real Binary Agent Process Hardening](../integration-issues/real-binary-agent-process-hardening.md)
- Sibling pattern for the persisted-PID side of process control: [Verify OS process identity before signaling a persisted PID](./verify-process-identity-before-signaling-pid.md)
