---
title: "Real Binary Agent Process Hardening"
date: 2026-06-07
category: integration-issues
module: orkestra-agent-runtime
problem_type: integration_issue
component: assistant
symptoms:
  - "`stop` could signal a stale or recycled PID because persisted process records did not prove process identity"
  - "Shell environment capture timeouts could leave background descendant processes alive"
  - "LSP reference formatting and server startup had avoidable repeated work and lock contention"
  - "CI and binary validation did not prove the compiled CLI worked with a real agent binary"
root_cause: logic_error
resolution_type: code_fix
severity: high
related_components:
  - tooling
  - testing-framework
tags: [process-identity, binary-e2e, lsp, shell-env, ci, claude-code]
---

# Real Binary Agent Process Hardening

## Problem

Orkestra had passed source-level tests, but several review findings showed that the compiled CLI still needed stronger guarantees at its real integration boundaries: OS process identity, shell timeout cleanup, LSP server pooling, CI tooling, and end-to-end agent execution. The risk was practical: `stop` could target the wrong process, shell capture could leak descendants, and a mocked binary test could miss failures in the built `orkestra` command.

## Symptoms

- `stop` relied on persisted PID/PGID state without proving the process still belonged to the run that recorded it.
- Shell environment capture used a timeout but did not reliably kill a timed-out shell's background descendants.
- LSP tools repeatedly read source files for reference formatting, and the LSP pool could hold its global mutex while starting a server.
- The binary E2E path initially proved orchestration against a fake `claude`, not the real installed Claude Code binary.

## What Didn't Work

- Persisting a wall-clock timestamp near process start was not enough process identity. It described when Orkestra launched the command, but it did not prove that a later PID still belonged to the same OS process.
- Killing only the timed-out shell process did not bound descendants started by shell startup scripts or test shells.
- Unit tests and fake-agent integration tests were useful for parser isolation, but they did not prove that the compiled `./orkestra` binary could create workspaces, call the real agent CLI, preserve JSON output, and handle edge cases.
- Recompiling `golangci-lint` in CI worked functionally, but it made the review gate slower and more brittle than using the maintained action cache path.

## Solution

The fix tightened each integration boundary and added a real binary E2E script.

### Verify process identity before signaling

`Runner.Run` now records an OS process-start identity token immediately after starting the agent. `Runner.IsRunning` and `Runner.Stop` validate PID, PGID, and start token together before treating a persisted process record as live. If the record is stale or recycled, `Stop` clears the process fields and marks the workspace inactive instead of signaling anything.

```go
startedAt, err := process.StartedAt(pid)
if err != nil {
    _ = process.TerminateGroup(pid, process.DefaultGrace)
    _ = cmd.Wait()
    return nil, fmt.Errorf("failed to capture agent process identity for workspace %s: %w", workspaceID, err)
}
if err := r.workspaceManager.SetSessionProcess(workspaceID, string(agent), pid, pid, startedAt); err != nil {
    _ = process.TerminateGroup(pid, process.DefaultGrace)
    _ = cmd.Wait()
    return nil, fmt.Errorf("failed to persist agent process for workspace %s: %w", workspaceID, err)
}
```

The platform-specific implementation reads the process start token from `/proc/<pid>/stat` on Linux and `ps -o lstart=` on macOS. That keeps the POSIX process-group model while avoiding recycled-PID kills.

### Kill timed-out shell capture as a process group

Shell environment capture starts the shell as a process-group leader, stores the child PID after `Start`, drains stdout, and kills the negative process group on timeout. The race detector caught an early shared-process access issue; storing the immutable PID after start fixed that race.

### Reduce LSP repeated work and startup contention

Reference formatting now uses a per-call source-line cache so repeated locations in the same response do not re-read the same file. The LSP pool now tracks in-flight server starts separately, so concurrent requests for the same server wait on the same start while unrelated servers do not sit behind the global pool mutex.

### Use maintained CI lint setup

The workflow now uses `golangci/golangci-lint-action@v8` with the pinned lint version instead of recompiling the linter on every run.

### Add a real binary E2E test

`scripts/binary-e2e.sh` builds a temporary Orkestra home, a real git repository with a bare origin, and exercises the compiled binary. It deliberately requires a real `claude` executable and does not place a fake agent earlier in `PATH`.

```bash
command -v claude >/dev/null || {
  echo "real claude binary is required for binary e2e" >&2
  exit 1
}

run_out="$("$BIN" --json run --workspace "$WS_ID" --agent claude --prompt "Reply exactly: ORKESTRA_BINARY_E2E_POSITIVE")"
session_id="$(printf '%s' "$run_out" | json_get session_id)"
[[ -n "$session_id" ]]
```

The script covers:

- Positive cases: `init`, workspace create/list, real Claude `run`, raw stream mode, real resume, and todo CRUD.
- Negative cases: missing repo, missing workspace, `mcp` without workspace, and wrong-agent resume.
- Edge cases: odd workspace-name slugging, dirty worktree removal requiring `--force`, and idempotent `stop` after the agent has exited.

## Why This Works

The fix moves Orkestra's safety checks to the same boundaries where failures occur. Process identity is verified against the operating system before any signal is sent. Shell timeout cleanup targets the process group, not just the immediate shell. LSP startup no longer serializes unrelated work behind a long-running start path. The E2E script now validates the actual compiled CLI and installed agent binary, so JSON cleanliness and resume behavior are proven through the same path a user runs.

## Prevention

- Treat persisted PIDs as untrusted until they match an OS-level identity token.
- When bounding shell commands that may spawn descendants, start them in their own process group and terminate the group on timeout.
- Keep binary E2E tests honest: call the compiled binary and real external CLI when the claim is "the binary works"; keep fake-agent tests only for isolated parser or failure-path coverage.
- Run both the normal CI mirror and the real binary E2E before accepting process-management changes:

```bash
rtk go build -o ./orkestra ./main.go
rtk make all
rtk ./scripts/binary-e2e.sh ./orkestra
```

Verified results for this fix:

- `rtk go build -o ./orkestra ./main.go` passed.
- `rtk make all` passed, including build, vet, lint, and race-enabled tests.
- `rtk ./scripts/binary-e2e.sh ./orkestra` passed with the real Claude Code binary.

## Related Issues

- Existing related learning: `docs/solutions/build-config/orkestra-phase2-parity.md`.
- Reusable patterns extracted from this incident:
  - `docs/solutions/design-patterns/verify-process-identity-before-signaling-pid.md`
  - `docs/solutions/design-patterns/kill-process-group-on-timeout.md`
  - `docs/solutions/design-patterns/single-flight-pooled-resource-init.md`
- Related GitHub issue search: no matching issues found for "orkestra process identity binary e2e".
