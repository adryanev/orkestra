# Requirements: Agent Tool Forwarding (`ask_user`)

**Date**: 2026-06-07  
**Branch**: `feature/agent-tool-forwarding`  
**Status**: Approved — ready for implementation

---

## 1. Problem

Spawned Claude Code agents have no way to ask the user a question mid-task. Interactive Claude Code tools (`AskUserQuestion`, etc.) error or fail silently because the spawned process has no human attached. Hermes (the parent orchestrator) has messaging platform integrations (Telegram, Discord, and others) to reach the user, but there is no mechanism to receive questions from agents and route answers back.

---

## 2. Goals

- Agents spawned by Orkestra can call a single MCP tool — `ask_user` — to ask the user a question.
- The agent is suspended until the user answers; no goroutines or file descriptors held open waiting.
- The user answers via whatever messaging platform Hermes is configured to use; Orkestra is platform-blind.
- The answer is delivered back into the resumed agent via prompt injection — no Claude Code session replay assumptions.
- Orkestra remains a pure file-state binary. It never calls Hermes; Hermes polls Orkestra's state directory.

## 3. Non-goals (v1)

- Multiple simultaneous pending questions per workspace.
- Orkestra-enforced timeouts (Hermes enforces the 24 h expiry).
- A `waiting_user_input` workspace status field.
- Rich platform-specific formatting (images, links, markdown) — `ask_user` is plain text.
- Windows path compatibility (follows existing `logs/` conventions throughout).
- Forwarding any tool other than `ask_user`.

---

## 4. Design Decisions

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Blocking model | **Agent suspension** | Messaging platform latency is unbounded (30 s to hours). Holding an open MCP connection risks transport timeouts and wastes resources. |
| D2 | Coupling model | **Pull — Hermes watches files** | Keeps Orkestra dependency-free (zero webhooks, no Hermes config, no platform knowledge). Hermes already has file-watching infrastructure. File state is grep-able and human-inspectable. |
| D3 | Answer delivery | **Prompt injection on resume** | Claude Code may not replay the `ask_user` call on resume. Injecting the answer as a continuation prompt is reliable across all agent behaviors. |
| D4 | Tool scope | **Single `ask_user` tool** | One code path, one platform-agnostic payload schema for v1. `confirm` (yes/no) and `choose` (multi-option) are both expressible as `ask_user` with appropriate `options`. |
| D5 | Platform coupling | **Orkestra is platform-blind** | Orkestra's pending file contains only question + options — no platform, no chat_id, no message_id. Hermes owns all platform routing config and dispatch state. Adding Discord or a new platform never requires an Orkestra change. |

---

## 5. System Architecture

```
┌─────────────────────────────────────────────────┐
│  Agent (Claude Code, spawned by Orkestra)        │
│                                                  │
│  calls MCP tool: ask_user(id, question, options) │
└───────────────────┬─────────────────────────────┘
                    │ MCP stdio
                    ▼
┌─────────────────────────────────────────────────┐
│  Orkestra MCP Server  (pkg/mcp/server.go)        │
│                                                  │
│  1. validate workspace binding                   │
│  2. reject if pending file already exists        │
│  3. write pending file (atomic rename)           │
│  4. return "Question forwarded…"                 │
└───────────────────┬─────────────────────────────┘
                    │ file write
                    ▼
       ~/.orkestra/pending/<workspace-id>.json
                    │
    ┌───────────────┴────────────────┐
    │ runner suspension watcher      │  (goroutine in executeAgent)
    │ polls pending file, 500 ms     │
    │ on detect → 500 ms grace →     │
    │ process.TerminateGroup(pid)    │
    └───────────────┬────────────────┘
                    │ SIGTERM
                    ▼
       Agent exits cleanly (Claude Code saves session)

                    ┌──────────────────────────────┐
                    │  Hermes  (parent orchestrator) │
                    │                               │
                    │  polls ~/.orkestra/pending/   │
                    │  parses question + options    │
                    │  looks up workspace routing   │
                    │    (platform + chat_id)       │
                    │  routes to platform adapter   │
                    │  stores dispatch metadata     │
                    │    in own state               │
                    └──────────────┬────────────────┘
                                   │ platform-native interaction
                                   │ (inline keyboard / select menu
                                   │  / reaction buttons / reply)
                                   ▼
                              User responds
                                   │
                    ┌──────────────┴────────────────┐
                    │  Hermes                        │
                    │  calls:                        │
                    │  orkestra resume               │
                    │    --workspace <id>            │
                    │    --agent claude              │
                    │    --answer "<text>"           │
                    └──────────────┬────────────────┘
                                   │
                    ┌──────────────┴────────────────┐
                    │  cmd/resume.go                 │
                    │  1. read pending file          │
                    │  2. build injection prompt     │
                    │  3. delete pending file        │
                    │  4. write answers/<id>.json    │
                    │  5. agentRunner.Run(resume=true│
                    │       prompt=injectionPrompt)  │
                    └──────────────┬────────────────┘
                                   │
                    ┌──────────────┴────────────────┐
                    │  Resumed agent                 │
                    │                               │
                    │  sees: "The user answered     │
                    │  your question '…': <answer>" │
                    │  continues task               │
                    └───────────────────────────────┘
```

---

## 6. State File Schemas

### 6.1 Pending question — `~/.orkestra/pending/<workspace-id>.json`

Written atomically by the MCP `ask_user` tool. Deleted by `orkestra resume --answer`.

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

| Field | Type | Required | Notes |
|---|---|---|---|
| `workspace_id` | string | yes | Must match the pending file's filename. |
| `question` | string | yes | Plain text. No length limit enforced by Orkestra. |
| `options` | []string | no | Empty or absent → Hermes renders platform-appropriate free-text input. Present → Hermes renders platform-appropriate choice UI (inline keyboard, select menu, etc.). |
| `asked_at` | RFC 3339 UTC | yes | Used by Hermes to enforce 24 h expiry. |
| `agent` | string | yes | `"claude"` or `"codex"`. |
| `session_id` | string | no | Empty for Codex (uses thread_id from sessions.json). |

**Atomic write protocol**: write to `<workspace-id>.json.tmp`, then `os.Rename` to `<workspace-id>.json`. This prevents Hermes from reading a partial file.

### 6.2 Answer record — `~/.orkestra/answers/<workspace-id>.json`

Written by `orkestra resume --answer`. Audit trail only — never read back by Orkestra.

```json
{
  "workspace_id": "abc123",
  "answer": "Integration test",
  "answered_at": "2026-06-07T10:05:00Z"
}
```

---

## 7. New File — `pkg/mcp/pending.go`

Owns all pending/answer I/O. Nothing else in the codebase touches these files directly.

```go
package mcp

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"
)

var ErrAlreadyPending = errors.New("workspace already has a pending question")
var ErrNoPending      = errors.New("no pending question for workspace")

type PendingQuestion struct {
    WorkspaceID string    `json:"workspace_id"`
    Question    string    `json:"question"`
    Options     []string  `json:"options,omitempty"`
    AskedAt     time.Time `json:"asked_at"`
    Agent       string    `json:"agent"`
    SessionID   string    `json:"session_id,omitempty"`
}

type AnswerRecord struct {
    WorkspaceID string    `json:"workspace_id"`
    Answer      string    `json:"answer"`
    AnsweredAt  time.Time `json:"answered_at"`
}

func PendingPath(configDir, workspaceID string) string {
    return filepath.Join(configDir, "pending", workspaceID+".json")
}

func AnswerPath(configDir, workspaceID string) string {
    return filepath.Join(configDir, "answers", workspaceID+".json")
}

// WritePending writes q atomically. Returns ErrAlreadyPending if a pending
// file already exists for the workspace.
func WritePending(configDir string, q PendingQuestion) error {
    dir := filepath.Join(configDir, "pending")
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return fmt.Errorf("failed to create pending dir: %w", err)
    }
    dst := PendingPath(configDir, q.WorkspaceID)
    if _, err := os.Stat(dst); err == nil {
        return ErrAlreadyPending
    }
    tmp := dst + ".tmp"
    data, err := json.Marshal(q)
    if err != nil {
        return fmt.Errorf("failed to marshal pending question: %w", err)
    }
    if err := os.WriteFile(tmp, data, 0o644); err != nil {
        return fmt.Errorf("failed to write pending tmp: %w", err)
    }
    if err := os.Rename(tmp, dst); err != nil {
        _ = os.Remove(tmp)
        return fmt.Errorf("failed to rename pending file: %w", err)
    }
    return nil
}

// ReadPending parses the pending file. Returns ErrNoPending if absent.
func ReadPending(configDir, workspaceID string) (PendingQuestion, error) {
    data, err := os.ReadFile(PendingPath(configDir, workspaceID))
    if errors.Is(err, os.ErrNotExist) {
        return PendingQuestion{}, ErrNoPending
    }
    if err != nil {
        return PendingQuestion{}, fmt.Errorf("failed to read pending file: %w", err)
    }
    var q PendingQuestion
    if err := json.Unmarshal(data, &q); err != nil {
        return PendingQuestion{}, fmt.Errorf("failed to parse pending file: %w", err)
    }
    return q, nil
}

// DeletePending removes the pending file. Idempotent.
func DeletePending(configDir, workspaceID string) error {
    err := os.Remove(PendingPath(configDir, workspaceID))
    if errors.Is(err, os.ErrNotExist) {
        return nil
    }
    return err
}

// WriteAnswer writes the answer audit record.
func WriteAnswer(configDir string, a AnswerRecord) error {
    dir := filepath.Join(configDir, "answers")
    if err := os.MkdirAll(dir, 0o755); err != nil {
        return fmt.Errorf("failed to create answers dir: %w", err)
    }
    data, err := json.Marshal(a)
    if err != nil {
        return fmt.Errorf("failed to marshal answer record: %w", err)
    }
    return os.WriteFile(AnswerPath(configDir, a.WorkspaceID), data, 0o644)
}
```

---

## 8. MCP Tool — `ask_user`

### 8.1 Registration (`pkg/mcp/server.go` — `register` function)

```go
mcp.AddTool(srv, &mcp.Tool{
    Name:        "ask_user",
    Description: "Ask the user a question and suspend this agent until they answer via Telegram. Provide options[] for a multiple-choice keyboard; omit options for free-text input.",
}, s.askUser)
```

### 8.2 Input type

```go
type askUserInput struct {
    ID       string   `json:"id"                jsonschema:"registered workspace id"`
    Question string   `json:"question"          jsonschema:"question to ask the user"`
    Options  []string `json:"options,omitempty" jsonschema:"answer choices; omit for free-text"`
}
```

### 8.3 Handler

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
        Agent:       ws.Agent,
        SessionID:   sess.SessionID,
    }
    if err := WritePending(s.wm.ConfigDir(), q); err != nil {
        return nil, messageOutput{}, fmt.Errorf("failed to forward question: %w", err)
    }
    msg := "Question forwarded to user. This agent will be suspended and resumed when the user answers."
    return text(msg), messageOutput{Message: msg}, nil
}
```

**Error surfaces**:
- `requireBoundWorkspace` fails → tool error (wrong workspace ID)
- `in.Question == ""` → tool error
- `WritePending` returns `ErrAlreadyPending` → tool error: `"workspace <id> already has a pending question"`

---

## 9. Runner — Suspension Watcher

**File**: `pkg/runner/claude_runner.go` — `executeAgent` function

Add a third goroutine after the PID persistence block (after `SetSessionProcess` succeeds), alongside the existing stdout and stderr goroutines:

```go
// Suspension watcher: when the MCP ask_user tool writes a pending question,
// terminate the agent so it can be resumed with the answer injected.
pendingPath := mcp.PendingPath(r.workspaceManager.ConfigDir(), workspaceID)
wg.Add(1)
go func() {
    defer wg.Done()
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for range ticker.C {
        if _, statErr := os.Stat(pendingPath); statErr == nil {
            // Grace period: let the MCP tool's response drain to the agent
            // before the process disappears.
            time.Sleep(500 * time.Millisecond)
            _ = process.TerminateGroup(pid, process.DefaultGrace)
            return
        }
        // Exit when the agent is no longer alive (stdout/stderr will also
        // drain to EOF), so the watcher goroutine does not outlive the run.
        if !process.IdentityMatches(pid, pid, startedAt) {
            return
        }
    }
}()
```

The existing `wg.Wait()` → `cmd.Wait()` → `CompleteSession` sequence is **unchanged**. The watcher goroutine joins `wg` so the wait is safe.

**Timing**: poll every 500 ms → detect file → 500 ms grace → SIGTERM. Worst-case latency from `ask_user` tool call to agent exit: ~1.5 s. Acceptable for async Telegram UX.

---

## 10. CLI — `cmd/resume.go`

### 10.1 New flag

```go
var resumeAnswer string

// in init():
resumeCmd.Flags().StringVar(&resumeAnswer, "answer", "", "User's answer to a pending ask_user question; injects the answer into the resume prompt")
```

### 10.2 Answer injection logic

In the `Run` handler, between prompt resolution and `agentRunner.Run`:

```go
if resumeAnswer != "" {
    pending, err := mcp.ReadPending(agentRunner.ConfigDir(), resumeWorkspace)
    if err != nil {
        emitError(fmt.Errorf("--answer provided but no pending question for workspace %s: %w", resumeWorkspace, err))
    }
    if err := mcp.DeletePending(agentRunner.ConfigDir(), resumeWorkspace); err != nil {
        emitError(fmt.Errorf("failed to clear pending question for workspace %s: %w", resumeWorkspace, err))
    }
    if err := mcp.WriteAnswer(agentRunner.ConfigDir(), mcp.AnswerRecord{
        WorkspaceID: resumeWorkspace,
        Answer:      resumeAnswer,
        AnsweredAt:  time.Now().UTC(),
    }); err != nil {
        // Non-fatal: audit trail failure should not block the resume.
        fmt.Fprintf(os.Stderr, "Warning: failed to write answer record: %v\n", err)
    }
    prompt = fmt.Sprintf("The user answered your question %q: %s", pending.Question, resumeAnswer)
}
```

The mutated `prompt` is then passed to `agentRunner.Run(resumeWorkspace, a, prompt, true, ...)` unchanged.

### 10.3 Runner `ConfigDir` accessor

`Runner` needs to expose the config directory for the resume command:

```go
// in pkg/runner/claude_runner.go
func (r *Runner) ConfigDir() string {
    return r.workspaceManager.ConfigDir()
}
```

---

## 11. Hermes Contract (Pull Interface)

Orkestra makes **zero assumptions** about Hermes. The entire contract is file-based.

### 11.1 Files

| File | Written by | Read by | Deleted by |
|---|---|---|---|
| `~/.orkestra/pending/<ws>.json` | Orkestra MCP tool (`ask_user`) | Hermes | `orkestra resume --answer` |
| `~/.orkestra/answers/<ws>.json` | `orkestra resume --answer` | (audit only) | never (manual cleanup) |

### 11.2 Hermes responsibilities

1. **Poll** `~/.orkestra/pending/` on a short interval (5–15 s recommended).
2. **On new file**: parse `question` and `options`.
   - `options` present → send Telegram **inline keyboard message**, single column (one button per row), up to 5–6 buttons visible.
   - `options` absent or empty → send Telegram **text prompt**; enable reply-threading.
3. **Store** Telegram `message_id` → `workspace_id` mapping in Hermes's own state (not in Orkestra files).
4. **Accept only replies** to the specific question `message_id` for free-text responses (prevents mis-routed answers in busy chats).
5. **Enforce 24 h expiry**: if `time.Now() - asked_at > 24h` and no answer received, discard the pending file or take a configured action. Orkestra has no timeout logic.
6. **On user response**: call:
   ```
   orkestra resume \
     --workspace <workspace_id> \
     --agent <agent> \
     --answer "<user_response_text>"
   ```

### 11.3 Telegram UX

| Scenario | Telegram message type | Input mechanism |
|---|---|---|
| `options` = `["Yes", "No"]` | Inline keyboard | Button tap |
| `options` = `["Option A", "Option B", "Option C"]` | Inline keyboard, single column | Button tap |
| `options` absent/empty | Text message with instructions | Reply to that message |

Button labels are truncated by Telegram at 64 characters if necessary. Orkestra does not enforce a length limit.

---

## 12. Error Cases

| Scenario | Location | Behavior |
|---|---|---|
| `ask_user` called with no `question` | MCP handler | Tool error: `"question is required"` |
| `ask_user` called when pending file exists | `WritePending` | Tool error: `"workspace <id> already has a pending question"` |
| Agent exits naturally before watcher fires | Suspension watcher | Watcher exits on identity-mismatch; pending file (if any) remains for Hermes |
| `orkestra resume --answer` with no pending file | `cmd/resume.go` | Fatal error: `"--answer provided but no pending question for workspace <id>"` |
| `orkestra resume --answer` answer record write fails | `cmd/resume.go` | Warning to stderr; resume proceeds (audit trail is non-fatal) |
| Hermes never delivers an answer | — | Pending file stays on disk; workspace remains suspended. `orkestra workspace list` shows workspace as `inactive` (process already exited). Hermes is responsible for timeout/retry logic. |
| Codex agent calls `ask_user` | MCP handler | Same path; `SessionID` field is empty in the pending file (Codex uses thread_id, stored in sessions.json). Resume works via Codex's thread_id from `GetSession`. |

---

## 13. Affected Files

```
pkg/mcp/
  pending.go          ← NEW: PendingQuestion, AnswerRecord, path helpers, I/O functions
  server.go           ← ADD: askUserInput type, askUser handler, register() entry

pkg/runner/
  claude_runner.go    ← ADD: suspension watcher goroutine in executeAgent, ConfigDir() accessor

cmd/
  resume.go           ← ADD: --answer flag, injection logic
```

No changes to:
- `pkg/workspace/` — workspace state schema unchanged
- `pkg/process/` — TerminateGroup already exists and is reused
- `cmd/run.go` — run command unchanged
- `cmd/mcp.go` — mcp server startup unchanged

---

## 14. Testing

| Test | Location | What to verify |
|---|---|---|
| `WritePending` atomic write | `pkg/mcp/pending_test.go` | File appears only after rename; no partial reads |
| `WritePending` rejects duplicate | `pkg/mcp/pending_test.go` | Returns `ErrAlreadyPending` when file exists |
| `ReadPending` / `DeletePending` | `pkg/mcp/pending_test.go` | Round-trip; delete is idempotent |
| `askUser` MCP handler | `pkg/mcp/server_test.go` | Writes pending file; returns correct message; rejects wrong workspace |
| Suspension watcher terminates agent | `pkg/runner/claude_runner_test.go` | Fake agent + pending file appears mid-run → `Run()` returns with agent killed |
| `orkestra resume --answer` | `cmd/resume_test.go` (or integration) | Pending file read → deleted → answer file written → prompt injected correctly |
| `orkestra resume --answer` no pending | `cmd/resume_test.go` | Exits with error, no session started |
