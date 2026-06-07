# Agent Tool Forwarding — `ask_user`

## Problem

Spawned agents (Claude Code, Codex) cannot use interactive tools like `AskUserQuestion`. These tools either fail silently or error out because the agent process has no path back to a live human. The goal is a mechanism for spawned agents to ask questions that route to the user via Hermes/Telegram, with the answer delivered when the agent resumes.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Wait model | **Agent suspension** | Telegram latency is unbounded; blocking the MCP connection risks transport timeouts |
| Coupling | **Pull (Hermes watches Orkestra)** | Orkestra stays generic (file state, no external deps); Hermes already has polling infrastructure |
| Answer delivery | **Prompt injection** | Doesn't depend on Claude Code replaying the `ask_user` tool call on resume (unreliable) |
| Tool scope | **Single `ask_user` tool** | One code path, one file schema, one Telegram message type; confirmations and free-form are expressed via options |

## Architecture

```
Spawned Agent (Claude Code)
  │  MCP tool call: ask_user(question, options[])
  ▼
Orkestra MCP Server
  │  writes ~/.orkestra/pending/<workspace-id>.json  (atomic write-rename)
  │  returns tool result: "Question forwarded. Agent will be suspended."
  ▼
Runner suspension watcher (goroutine in executeAgent)
  │  detects pending file → waits 500ms grace → SIGTERM agent process group
  ▼
Agent exits — Claude Code saves session before dying
  │
  └──────────────────────────────────────────────────────────┐
                                                             │
Hermes polls ~/.orkestra/pending/                           │
  │  detects file → parses question + options               │
  │  sends Telegram inline keyboard (options) or            │
  │  reply-threaded message (free-form, no options)         │
  │  user responds                                          │
  │  writes ~/.orkestra/answers/<workspace-id>.json         │
  │  calls: orkestra resume <workspace-id> --answer "..."   │
  ▼
Orkestra resume command
  │  reads pending file (gets original question text)
  │  builds injection: "User answered your question '{question}': {answer}"
  │  deletes pending file  →  writes answers file (audit)
  │  resumes agent with injection appended as continuation prompt
  ▼
Resumed Agent continues with answer in context
```

---

## State Files

### Pending question — `~/.orkestra/pending/<workspace-id>.json`

Written by the MCP `ask_user` handler. Deleted by `orkestra resume --answer`. Immutable once written (Hermes must not modify it).

```json
{
  "workspace_id": "abc123",
  "question": "Which test approach should I use?",
  "options": ["Integration test", "Unit test with mocks", "Both"],
  "asked_at": "2026-06-07T10:00:00Z",
  "agent": "claude",
  "session_id": "sess_abc123"
}
```

- `options` empty array → free-form text input (Hermes renders reply-threaded prompt)
- `options` non-empty → inline keyboard, one button per row, max ~6 buttons

### Answer audit log — `~/.orkestra/answers/<workspace-id>.json`

Written by `orkestra resume --answer` after consuming the pending file. Never read by Orkestra; for Hermes/human debugging only.

```json
{
  "workspace_id": "abc123",
  "question": "Which test approach should I use?",
  "answer": "Integration test",
  "answered_at": "2026-06-07T10:05:00Z"
}
```

---

## Component 1: MCP Tool — `ask_user`

**File**: `pkg/mcp/server.go`

```go
type askUserInput struct {
    ID       string   `json:"id"                jsonschema:"registered workspace id"`
    Question string   `json:"question"          jsonschema:"question to ask the user"`
    Options  []string `json:"options,omitempty" jsonschema:"answer choices; omit for free-text input"`
}
```

**Handler behavior** (in order):

1. `requireBoundWorkspace(in.ID)` — reject cross-workspace calls
2. Reject if `pending/<id>.json` already exists — one pending question per workspace
3. Write pending JSON file **atomically**: write to `pending/<id>.json.tmp`, then `os.Rename` to `pending/<id>.json`
4. Return tool result text: `"Question forwarded to user. This agent will be suspended and resumed with the answer."`

The tool returns successfully before the agent is killed. The agent records a clean tool result in its session, so on resume the conversation is in a consistent state.

**Registration** (add to `register()` in `server.go`):
```go
mcp.AddTool(srv, &mcp.Tool{
    Name:        "ask_user",
    Description: "Ask the user a question. The agent will be suspended until the user answers. Use options[] for multiple choice; omit options for free-text.",
}, s.askUser)
```

---

## Component 2: Pending File Writer

**File**: `pkg/mcp/pending.go` (new)

Handles atomic write and deduplication. Returns an `AlreadyPendingError` if a question is already waiting.

```go
func writePending(configDir, workspaceID, question string, options []string, agent, sessionID string) error
func pendingPath(configDir, workspaceID string) string   // ~/.orkestra/pending/<id>.json
func answersPath(configDir, workspaceID string) string   // ~/.orkestra/answers/<id>.json
```

---

## Component 3: Runner Suspension Watch

**File**: `pkg/runner/claude_runner.go`, inside `executeAgent()`

A watcher goroutine added alongside the existing stdout/stderr goroutines. It exits when either:
- The pending file appears (→ kills the agent), or
- The agent process exits normally (signalled via a `done` channel closed by the stdout goroutine on EOF)

```go
done := make(chan struct{})

// Existing stdout goroutine — add defer close(done)
wg.Add(1)
go func() {
    defer wg.Done()
    defer close(done)          // ← signals watcher that agent exited
    // ... existing reader code unchanged
}()

// Existing stderr goroutine unchanged.

// New: suspension watcher (not in wg — fire-and-forget)
pendingFile := filepath.Join(r.workspaceManager.ConfigDir(), "pending", workspaceID+".json")
go func() {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-done:
            return // agent exited normally; nothing to do
        case <-ticker.C:
            if _, err := os.Stat(pendingFile); err == nil {
                // Give the MCP tool 500ms to finish returning its result
                // before the process group is terminated.
                time.Sleep(500 * time.Millisecond)
                _ = process.TerminateGroup(pid, process.DefaultGrace)
                return
            }
        }
    }
}()

wg.Wait()   // drains stdout+stderr after process dies
```

The watcher is NOT added to `wg`. `wg.Wait()` unblocks once the agent's pipes drain (which happens once the process is dead), regardless of whether the watcher caused the death or the agent exited on its own.

---

## Component 4: Resume Command Extension

**File**: `cmd/resume.go`

New flag: `--answer <text>`

When `--answer` is provided:

1. Stat `~/.orkestra/pending/<workspace-id>.json` — error if missing (no pending question)
2. Parse pending file to get original `question` text
3. Build injection prompt:
   ```
   The user answered your question "{question}": {answer}
   ```
4. Delete `pending/<workspace-id>.json`
5. Write `answers/<workspace-id>.json` (audit trail)
6. Append injection as the `--prompt` argument for the resume `Run()` call

The injection prompt becomes the first message the agent sees on resume, before it reads any new task. The agent's prior context shows it asked a question; the injection delivers the answer and it continues.

---

## Component 5: Hermes Pull Contract

Orkestra makes no assumptions about Hermes. The contract is defined purely by file paths and the `orkestra resume` CLI.

| File | Written by | Read by | Deleted by |
|---|---|---|---|
| `pending/<ws>.json` | Orkestra MCP | Hermes | `orkestra resume --answer` |
| `answers/<ws>.json` | `orkestra resume` | (audit) | never |

**Hermes responsibilities**:

- Poll `~/.orkestra/pending/` directory on an interval (suggested: 5–10s)
- On new file: parse `question` + `options`
  - `options` non-empty → send Telegram inline keyboard, **one button per row**, single column layout
  - `options` empty → send Telegram message, require user to **reply-thread** to it (prevents accidental answers in busy chats)
- Track the sent `message_id` in Hermes-side state (not written to the Orkestra pending file)
- On user response: call `orkestra resume <workspace-id> --answer "<selected-or-typed-text>"`
- **24-hour timeout**: if no reply after 24h, Hermes may either send a reminder or drop the question (Hermes policy, not Orkestra's)

---

## Out of Scope for v1

- Workspace status field `waiting_user_input` — the workspace stays `active` (agent process has exited, but session exists; status semantics are a separate concern)
- Multiple concurrent pending questions per workspace — rejected with an error by the MCP tool
- Codex agent support — `ask_user` registers in the MCP server which both agents use; Codex gets the tool for free, but suspension/resume semantics are untested
- Windows path separators — `pending/` and `answers/` follow the same conventions as the existing `logs/` directory

---

## File Checklist

| File | Change |
|---|---|
| `pkg/mcp/pending.go` | New — atomic write, path helpers |
| `pkg/mcp/server.go` | Register `ask_user` tool; add `askUser` handler |
| `pkg/runner/claude_runner.go` | Add suspension watcher goroutine + `done` channel in `executeAgent` |
| `cmd/resume.go` | Add `--answer` flag; pending read/delete/inject logic |
