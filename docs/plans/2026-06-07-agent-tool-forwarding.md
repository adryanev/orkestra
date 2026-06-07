# Agent Tool Forwarding — `ask_user`

**Branch**: `feature/agent-tool-forwarding`  
**Date**: 2026-06-07  
**Status**: Ready for implementation

## Problem

Spawned Claude Code agents have no way to ask the user a question mid-task. Interactive Claude Code tools (`AskUserQuestion`, etc.) either error or fail silently because there is no human attached to the spawned process. Hermes (the parent orchestrator) has a Telegram channel to the user, but no mechanism to receive questions from agents and route answers back.

## Design decisions (from brainstorm)

| Decision | Choice | Rationale |
|---|---|---|
| Blocking model | Agent suspension | Telegram latency is unbounded; blocking the MCP connection would risk transport timeouts |
| Coupling model | Pull (Hermes watches files) | Keeps Orkestra dependency-free; no webhooks, no Hermes config in Orkestra |
| Answer delivery | Prompt injection on resume | Doesn't rely on Claude Code replaying the `ask_user` call; works with existing resume machinery |
| Tool scope | Single `ask_user` tool | One code path, one file schema, one Telegram message type for v1 |

## End-to-end flow

```
Agent (Claude Code)
  │
  │  MCP tool call: ask_user(id, question, options[])
  ▼
Orkestra MCP server (pkg/mcp/server.go)
  │  1. Validate workspace binding
  │  2. Reject if pending file already exists
  │  3. Write ~/.orkestra/pending/<workspace-id>.json  (atomic rename)
  │  4. Return: "Question forwarded. Agent will be suspended and resumed with your answer."
  │
  │  (runner suspension watcher goroutine sees the pending file)
  │  5. Wait 500 ms grace (MCP response drains to agent)
  │  6. process.TerminateGroup(pid, process.DefaultGrace)
  │
Agent exits — Claude Code saves session state on exit
  │
  ▼
~/.orkestra/pending/<workspace-id>.json   ◄── Hermes polls this directory
  │
  │  Hermes detects file → sends Telegram message
  │    • options present → inline keyboard, single-column, one button per row
  │    • no options      → text prompt, reply-thread scoped to that message
  │  Hermes stores Telegram message_id in its own state
  │  User responds (24 h timeout)
  │
  │  Hermes calls:
  │    orkestra resume --workspace <id> --agent claude --answer "<selected text>"
  ▼
cmd/resume.go  (new --answer flag)
  │  1. Read ~/.orkestra/pending/<workspace-id>.json  (error if missing)
  │  2. Build injection: "The user answered your question '<question>': <answer>"
  │  3. Delete pending file
  │  4. Write answer to ~/.orkestra/answers/<workspace-id>.json  (audit trail)
  │  5. Call agentRunner.Run(..., resume=true, injectionPrompt, ...)
  ▼
Resumed agent receives answer in continuation prompt and continues
```

## State files

### `~/.orkestra/pending/<workspace-id>.json`

Written atomically by the MCP tool. Deleted by `orkestra resume --answer`.

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

- `options` empty array or omitted → Hermes renders a free-text Telegram prompt instead of buttons.
- `session_id` lets `orkestra resume --answer` verify it is resuming the correct session.

### `~/.orkestra/answers/<workspace-id>.json`

Written by `orkestra resume --answer` after reading the pending file. Audit trail only; never read by Orkestra after creation.

```json
{
  "workspace_id": "abc123",
  "answer": "Integration test",
  "answered_at": "2026-06-07T10:05:00Z"
}
```

## Implementation units

### 1. State file types — `pkg/mcp/pending.go` (new)

```go
type PendingQuestion struct {
    WorkspaceID string    `json:"workspace_id"`
    Question    string    `json:"question"`
    Options     []string  `json:"options,omitempty"`
    AskedAt     time.Time `json:"asked_at"`
    Agent       string    `json:"agent"`
    SessionID   string    `json:"session_id"`
}

type AnswerRecord struct {
    WorkspaceID string    `json:"answer_workspace_id"`
    Answer      string    `json:"answer"`
    AnsweredAt  time.Time `json:"answered_at"`
}

// PendingPath returns ~/.orkestra/pending/<workspace-id>.json
func PendingPath(configDir, workspaceID string) string

// AnswerPath returns ~/.orkestra/answers/<workspace-id>.json
func AnswerPath(configDir, workspaceID string) string

// WritePending writes the pending question atomically (write tmp, rename).
// Returns ErrAlreadyPending if a pending file already exists for this workspace.
func WritePending(configDir string, q PendingQuestion) error

// ReadPending reads and parses the pending file. Returns ErrNoPending if absent.
func ReadPending(configDir, workspaceID string) (PendingQuestion, error)

// DeletePending removes the pending file. Idempotent (no error if already gone).
func DeletePending(configDir, workspaceID string) error

// WriteAnswer writes the answer audit record, creating the answers dir if needed.
func WriteAnswer(configDir string, a AnswerRecord) error
```

### 2. New MCP tool — `pkg/mcp/server.go`

Add to `register()`:

```go
mcp.AddTool(srv, &mcp.Tool{
    Name:        "ask_user",
    Description: "Ask the user a question and suspend this agent until they answer via Telegram. Options present → inline keyboard; no options → free-text reply.",
}, s.askUser)
```

Input type:

```go
type askUserInput struct {
    ID       string   `json:"id"               jsonschema:"registered workspace id"`
    Question string   `json:"question"         jsonschema:"question to ask the user"`
    Options  []string `json:"options,omitempty" jsonschema:"answer choices; omit for free-text input"`
}
```

Handler:

```go
func (s *Server) askUser(ctx context.Context, _ *mcp.CallToolRequest, in askUserInput) (*mcp.CallToolResult, messageOutput, error) {
    if err := s.requireBoundWorkspace(in.ID); err != nil {
        return nil, messageOutput{}, err
    }
    if in.Question == "" {
        return nil, messageOutput{}, fmt.Errorf("question is required")
    }
    ws, err := s.wm.GetWorkspace(in.ID)
    if err != nil {
        return nil, messageOutput{}, fmt.Errorf("failed to get workspace: %w", err)
    }
    sess, _ := s.wm.GetSession(in.ID)
    q := PendingQuestion{
        WorkspaceID: in.ID,
        Question:    in.Question,
        Options:     in.Options,
        AskedAt:     time.Now().UTC(),
        Agent:       ws.Agent,   // or from session
        SessionID:   sess.SessionID,
    }
    if err := WritePending(s.wm.ConfigDir(), q); err != nil {
        return nil, messageOutput{}, fmt.Errorf("failed to write pending question: %w", err)
    }
    msg := "Question forwarded to user. This agent will be suspended and resumed when the user answers."
    return text(msg), messageOutput{Message: msg}, nil
}
```

### 3. Runner suspension watcher — `pkg/runner/claude_runner.go`

In `executeAgent`, add a third goroutine alongside the existing stdout/stderr goroutines, started after `cmd.Start()` and PID persistence:

```go
// Suspension watcher: terminate the agent when a pending question is written.
pendingPath := mcp.PendingPath(r.workspaceManager.ConfigDir(), workspaceID)
wg.Add(1)
go func() {
    defer wg.Done()
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for range ticker.C {
        if _, err := os.Stat(pendingPath); err == nil {
            // Grace period: let the MCP tool response drain to the agent.
            time.Sleep(500 * time.Millisecond)
            _ = process.TerminateGroup(pid, process.DefaultGrace)
            return
        }
        // Exit when the process is gone (stdout/stderr goroutines will also exit).
        if !process.IdentityMatches(pid, pid, startedAt) {
            return
        }
    }
}()
```

The watcher exits when either the pending file appears (and it kills the agent) or the agent exits naturally (identity no longer matches). The existing `wg.Wait()` → `cmd.Wait()` sequence is unchanged.

### 4. CLI change — `cmd/resume.go`

Add flag:

```go
var resumeAnswer string
// in init():
resumeCmd.Flags().StringVar(&resumeAnswer, "answer", "", "User's answer to a pending ask_user question")
```

In the `Run` handler, before calling `agentRunner.Run`, handle answer injection:

```go
if resumeAnswer != "" {
    pending, err := mcp.ReadPending(agentRunner.ConfigDir(), resumeWorkspace)
    if err != nil {
        emitError(fmt.Errorf("--answer provided but no pending question for workspace %s: %w", resumeWorkspace, err))
    }
    if err := mcp.DeletePending(agentRunner.ConfigDir(), resumeWorkspace); err != nil {
        emitError(fmt.Errorf("failed to clear pending question: %w", err))
    }
    _ = mcp.WriteAnswer(agentRunner.ConfigDir(), mcp.AnswerRecord{
        WorkspaceID: resumeWorkspace,
        Answer:      resumeAnswer,
        AnsweredAt:  time.Now().UTC(),
    })
    prompt = fmt.Sprintf("The user answered your question %q: %s", pending.Question, resumeAnswer)
}
```

### 5. Workspace manager accessor

`Runner` needs `ConfigDir()` exposed, or the pending/answer path helpers need `wm.ConfigDir()`. `workspace.Manager` already has `ConfigDir()` — expose it on `Runner`:

```go
func (r *Runner) ConfigDir() string {
    return r.workspaceManager.ConfigDir()
}
```

## Hermes contract (pull interface)

Orkestra makes no assumptions about Hermes. The contract is purely file-based:

| File | Written by | Deleted by | Purpose |
|---|---|---|---|
| `pending/<ws>.json` | Orkestra MCP tool | `orkestra resume --answer` | Question payload for Hermes |
| `answers/<ws>.json` | `orkestra resume --answer` | never (manual) | Audit trail |

**Hermes responsibilities:**

1. Poll `~/.orkestra/pending/` on a short interval (5–15 s is fine; Telegram UX is async)
2. On new file: parse question + options
   - Options present → send Telegram inline keyboard message, **single column** (one button per row), max 5–6 visible
   - No options → send Telegram text prompt; enable reply-threading (track `message_id`)
3. Accept only replies to the specific question `message_id` for free-text answers (prevents stale/mis-routed responses)
4. Question expires after **24 hours** with no reply (Hermes responsibility to enforce)
5. On user response: call `orkestra resume --workspace <id> --agent claude --answer "<selected text>"`

Hermes stores `message_id` → `workspace_id` mapping in its own state. Orkestra's pending file has no Telegram fields.

## Error cases

| Scenario | Behavior |
|---|---|
| `ask_user` called when pending file already exists | MCP tool returns error: `"workspace <id> already has a pending question"` |
| `orkestra resume --answer` when no pending file | Command errors: `"--answer provided but no pending question for workspace <id>"` |
| Agent exits naturally before suspension watcher fires | Watcher exits on identity-mismatch check; pending file (if any) remains for Hermes |
| Hermes never delivers answer | Pending file stays on disk; workspace remains suspended indefinitely (v1: no auto-timeout in Orkestra) |
| Codex agent | Same file protocol; `buildAgentArgs` for Codex doesn't use session_id, so SessionID field is empty — resume uses thread_id from sessions.json as normal |

## Out of scope for v1

- Workspace status field `waiting_user_input` (status transitions: `active` → suspend → `inactive` via existing path; Hermes infers state from pending file presence)
- Orkestra-side 24 h timeout (Hermes enforces this)
- Multiple concurrent pending questions per workspace
- Windows path separators (`pending/` and `answers/` follow the same conventions as the existing `logs/` directory)
- Rich Telegram formatting (images, links) — `ask_user` is text-only

## File layout after this change

```
~/.orkestra/
  pending/
    <workspace-id>.json    ← written by ask_user MCP tool
  answers/
    <workspace-id>.json    ← written by orkestra resume --answer (audit)
  logs/
    <workspace-id>.log     ← existing notify output (unchanged)

pkg/mcp/
  pending.go               ← new: PendingQuestion, AnswerRecord, path helpers
  server.go                ← add askUser handler + register
cmd/
  resume.go                ← add --answer flag + injection logic
pkg/runner/
  claude_runner.go         ← add suspension watcher goroutine in executeAgent
```
