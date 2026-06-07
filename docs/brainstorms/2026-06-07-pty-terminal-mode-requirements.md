# PTY Terminal Mode for Orkestra

**Status:** Draft  
**Date:** 2026-06-07  
**Branch:** feature/pty-terminal-session

---

## Problem

Claude Code's `-p` flag (pipe mode) operates under separate, more restrictive rate limits than interactive terminal usage. Orkestra currently always invokes Claude Code with `-p` and `--output-format stream-json`, meaning all agent runs are subject to pipe-mode rate limits regardless of intensity.

## Goal

Spawn Claude Code in a real PTY so it runs as an interactive terminal session, bypassing pipe-mode rate limits while Orkestra continues to manage workspace isolation, process lifecycle, and attach/detach.

---

## Core Use Case

```
orkestra run --pty --workspace <id> "refactor the auth module"
```

Orkestra injects the initial prompt into an interactive Claude Code session. The user can then:

```
orkestra attach --workspace <id>   # connect to the live session
# ... work interactively ...
# Ctrl+\ Ctrl+\                    # detach (session keeps running)
orkestra attach --workspace <id>   # re-attach later
orkestra stop --workspace <id>     # terminate when done
```

The PTY session outlives any individual `attach` — it behaves like a tmux session scoped to a workspace.

---

## Non-Goals (v1)

- Windows ConPTY support — PTY mode is Unix-only; Windows gets a clear error
- Scrollback/replay buffer — clients see live output from the attach point forward
- Multiple simultaneous `attach` clients — single-client only in v1
- Session ID capture from Claude's terminal output — not needed in the self-contained model
- tmux backend — deferred as a possible v2 alternative
- Web/browser attach — Unix socket only

---

## Architecture

### PTY Broker

A small, long-lived broker process is the core of this feature. It:

1. Opens a PTY master/slave pair (`pkg/pty/broker.go`)
2. Spawns Claude Code with the slave as its controlling terminal — **no `-p` flag, no `--output-format stream-json`**, with `--permission-mode bypassPermissions` and `--mcp-config <path>`
3. Injects the initial prompt by writing to the PTY master fd after a brief ready-wait
4. Listens on a Unix domain socket at `~/.orkestra/pty/<workspace-id>.sock`
5. Proxies raw bytes between the socket and PTY master in both directions
6. Intercepts the detach escape sequence (`Ctrl+\ Ctrl+\`) on incoming bytes before forwarding — on detection, closes the client connection without signalling Claude
7. Forwards SIGWINCH from the attached client's terminal to the Claude process
8. On SIGHUP or broker process exit, sends SIGHUP to Claude (standard PTY teardown)

The broker is launched as a daemonized child of `orkestra run --pty` — double-fork so `orkestra run` returns promptly once the broker is alive and Claude has started.

### Attach Client (`orkestra attach`)

The attach command:

1. Looks up the workspace's broker socket path from sessions.json
2. Verifies the broker process is alive
3. Connects to the Unix socket
4. Switches the user's terminal to raw mode
5. Runs a bidirectional copy loop: stdin → socket, socket → stdout
6. Installs a SIGWINCH handler to send window-size updates to the broker (which forwards them to Claude via `ioctl TIOCSWINSZ`)
7. On disconnect (socket closed by broker) or escape detection, restores terminal mode and exits

### Escape Sequence

**`Ctrl+\ Ctrl+\`** — two `0x1C` bytes within 500 ms.

- Chosen because `Ctrl+\` is rarely used in practice (Claude Code does not bind it), distinct from tmux (`Ctrl+B`) and screen (`Ctrl+A`)
- The broker detects this sequence in the client→PTY byte stream and closes the client socket instead of forwarding
- A brief status line is printed before disconnecting: `[orkestra] detached from workspace <id>`

---

## State Management

PTY session state is stored in the existing `sessions.json` under a new `pty` sub-object:

```json
{
  "<workspace-id>": {
    "agent": "claude",
    "model": "claude-sonnet-4-6",
    "pid": 12345,
    "pgid": 12345,
    "started_at": "...",
    "pty": {
      "broker_pid": 12344,
      "socket_path": "/home/user/.orkestra/pty/<workspace-id>.sock",
      "mode": true
    }
  }
}
```

- `broker_pid`: used by `orkestra stop` to terminate the broker (which triggers SIGHUP → Claude)
- `socket_path`: used by `orkestra attach` to find the live session
- `mode: true`: distinguishes PTY sessions from pipe-mode sessions — gates the `attach` command and suppresses NDJSON-based session ID capture

### Session Lifecycle

| Event | Action |
|---|---|
| `orkestra run --pty` | Spawns broker daemon, writes PTY state to sessions.json |
| `orkestra attach` | Reads socket_path, connects, bridges terminal |
| Detach (escape) | Broker closes client socket; broker + Claude continue |
| `orkestra stop` | Sends SIGTERM to broker_pid process group; broker exits; Claude gets SIGHUP |
| Claude exits naturally | Broker detects PTY EOF, closes socket, exits; sessions.json updated |

---

## Command Surface

### Modified: `orkestra run`

```
--pty    Spawn Claude Code in PTY mode instead of pipe mode.
         Returns immediately once the broker is running.
         Use `orkestra attach` to connect interactively.
```

Flags that become no-ops or errors in `--pty` mode:
- `--stream`: ignored (PTY output is not NDJSON)
- `--json` output of session info: emits workspace_id + broker_pid + socket_path (no session_id/usage)

### New: `orkestra attach`

```
orkestra attach --workspace <id>

Attach to a running PTY session for a workspace. Connects stdin/stdout
to the live Claude Code terminal. Detach with Ctrl+\ Ctrl+\.
```

Flags:
- `--workspace <id>` (required)

Exit codes:
- `0`: clean detach (user pressed escape)
- `1`: workspace not found, no PTY session, or broker not running

### Unchanged: `orkestra stop`

Existing stop logic handles PTY sessions via `broker_pid` — no command-level changes needed. The broker receives SIGTERM, Claude receives SIGHUP.

---

## Platform Support

| Platform | PTY mode |
|---|---|
| Linux | Supported |
| macOS | Supported |
| Windows | Returns error: "PTY mode is not supported on Windows; use pipe mode (omit --pty)" |

New files follow the existing `_unix.go` / `_windows.go` convention:
- `pkg/pty/broker_unix.go` — PTY allocation and broker logic
- `pkg/pty/broker_windows.go` — stub that returns `ErrNotSupported`

---

## Integration with Existing Runner

`buildAgentArgs` in `pkg/runner/claude_runner.go` gains a `ptyMode bool` parameter. When true:
- Omit `-p <prompt>` and `--output-format stream-json --verbose`
- Include `--permission-mode bypassPermissions`, `--mcp-config <path>`, model/effort flags as usual
- The prompt is injected via PTY write after spawn, not via CLI argument

`executeAgent` delegates to a new `executePTYAgent` path when `ptyMode` is set, which:
- Creates the broker
- Daemonizes it
- Records PTY state in sessions.json
- Returns immediately (no blocking stdout drain)

Pipe-mode path is unchanged.

---

## Success Criteria

1. `orkestra run --pty --workspace <id> "prompt"` returns within 2 seconds with the broker running
2. `orkestra attach --workspace <id>` connects and the user sees Claude Code's interactive UI
3. `Ctrl+\ Ctrl+\` detaches without killing Claude
4. Re-attaching after detach shows a live session (Claude Code still running)
5. `orkestra stop` cleanly terminates both broker and Claude
6. Claude Code exits naturally → broker and socket cleaned up automatically
7. Running `orkestra attach` on a workspace with no PTY session returns a clear error
8. Running `orkestra run --pty` on Windows returns a clear, actionable error message

---

## Open Questions

- **Initial prompt timing**: How long to wait before writing the prompt to the PTY? A fixed 200 ms delay is fragile; a better heuristic (e.g. wait for Claude's first output byte) may be needed.
- **Broker crash handling**: If the broker dies unexpectedly, sessions.json retains stale `pty` state. Should `orkestra attach` detect and clean this up (via `broker_pid` liveness check), or should a separate `orkestra status` surface it?
- **Signal forwarding**: Should the broker forward other signals (SIGINT from Ctrl+C inside the attached terminal) directly, or let the PTY handle it naturally via the slave's terminal discipline?
