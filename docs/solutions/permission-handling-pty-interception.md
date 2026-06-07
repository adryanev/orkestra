---
module: pkg/pty
tags: [permission-handling, pty, daemon, approval-workflow, security]
problem_type: feature
created: 2026-06-08
---

# Permission Handling: PTY Interception

## Problem

Orkestra needs to intercept agent permission prompts (from Claude Code, Codex, etc.) at the PTY level to enable programmatic approval workflows. This allows external interfaces (CLI, web UI, mobile apps) to handle approvals without being directly attached to the agent's TTY.

## Solution

Extended `pkg/pty/daemon.go` to intercept permission prompts by enhancing the PTY reader goroutine with:

1. **Line buffering** - Buffers PTY output into lines for pattern matching
2. **Prompt detection** - Uses `pkg/pty/parser.go` patterns to detect permission prompts
3. **Risk classification** - Classifies command risk using `pkg/pty/risk.go`
4. **State management** - Writes approval requests to filesystem state via `pkg/state/approval.go`
5. **Polling loop** - Waits for approval response with configurable timeout
6. **Response injection** - Injects "y" or "n" to PTY master stdin based on response
7. **Audit logging** - Logs all approval events via `pkg/audit/audit.go`

### Architecture

```
┌─────────────────────────────────────────────────┐
│ PTY Reader Goroutine (daemon.go)                │
│                                                  │
│  PTY Master → Line Buffer → Prompt Detection    │
│                                   ↓              │
│                          Risk Classification    │
│                                   ↓              │
│                     Write Approval Request       │
│                     (~/.orkestra/approvals/)     │
│                                   ↓              │
│                        Poll for Response         │
│                        (timeout: 5min)           │
│                                   ↓              │
│                   Inject "y" or "n" to PTY      │
│                                   ↓              │
│                          Audit Log               │
│                  (~/.orkestra/audit/)            │
└─────────────────────────────────────────────────┘
```

### Key Components

#### 1. Enhanced DaemonConfig

```go
type DaemonConfig struct {
    // ... existing fields
    ApprovalTimeout  time.Duration // Default: 5 minutes
    EnableAutoApprove bool          // Default: false (opt-in)
}
```

#### 2. Process with Prompt Detection

```go
func processWithPromptDetection(
    data []byte,
    lineBuffer *bytes.Buffer,
    workspaceID string,
    timeout time.Duration,
    master *os.File,
) []byte
```

Buffers PTY output into lines and detects prompts using `DetectPrompt()`. When a prompt is detected, triggers the approval workflow.

#### 3. Handle Approval Request

```go
func handleApprovalRequest(
    workspaceID string,
    agent string,
    command string,
    timeout time.Duration,
    master *os.File,
)
```

Orchestrates the complete approval workflow:
1. Classifies risk via `ClassifyRisk()`
2. Creates `ApprovalRequest` with UUID
3. Writes to `~/.orkestra/approvals/{workspace}_pending.json`
4. Polls for response file `~/.orkestra/approvals/{workspace}_response.json`
5. On response or timeout: injects "y" or "n"
6. Logs to audit trail
7. Cleans up state files

#### 4. Wait for Approval Response

```go
func waitForApprovalResponse(workspaceID string, timeout time.Duration) *state.ApprovalResponse
```

Polls for approval response with 100ms intervals. Returns `nil` on timeout.

#### 5. Inject Response

```go
func injectResponse(master *os.File, approved bool)
```

Writes "y\n" (approve) or "n\n" (reject) to PTY master stdin.

### Timeout Behavior

- **Default timeout**: 5 minutes (configurable via `DaemonConfig.ApprovalTimeout`)
- **On timeout**: Auto-rejects (injects "n")
- **Audit log**: Records timeout event with timestamp

### Audit Logging

All approval events are logged to `~/.orkestra/audit/approvals.jsonl` as newline-delimited JSON:

```json
{"timestamp":"2026-06-08T10:30:00Z","workspace_id":"ws-123","agent":"claude","command":"git push","risk_level":1,"event_type":"request","request_id":"uuid-123"}
{"timestamp":"2026-06-08T10:30:15Z","workspace_id":"ws-123","agent":"claude","command":"git push","risk_level":1,"event_type":"approve","request_id":"uuid-123","responded_by":"user@example.com"}
```

Event types:
- `request` - Approval request created
- `approve` - User approved
- `reject` - User rejected
- `timeout` - Request timed out (auto-rejected)

### Integration Points

The approval workflow integrates with:

1. **pkg/pty/parser.go** - Prompt pattern detection
2. **pkg/pty/risk.go** - Command risk classification (Safe/Moderate/Dangerous)
3. **pkg/state/approval.go** - Cross-process state management with file locking
4. **pkg/audit/audit.go** - Audit logging (new package)

### Enabling the Feature

The feature is **opt-in** via `DaemonConfig.EnableAutoApprove`:

```go
cfg := pty.DaemonConfig{
    // ... other fields
    EnableAutoApprove: true,
    ApprovalTimeout: 3 * time.Minute, // Optional: override default
}
```

When disabled (default), PTY output passes through unchanged.

### Testing

Comprehensive test coverage in `pkg/pty/approval_test.go`:

- `TestProcessWithPromptDetection` - Verifies prompt detection in buffered output
- `TestHandleApprovalRequest_AutoReject` - Verifies timeout behavior
- `TestHandleApprovalRequest_Approve` - Verifies approval flow
- `TestHandleApprovalRequest_Reject` - Verifies rejection flow
- `TestInjectResponse` - Verifies response injection

Run tests:
```bash
go test ./pkg/pty -run TestApproval
```

### Future Enhancements

1. **Auto-approval policies** - Auto-approve based on risk level and command patterns
2. **inotify/fsnotify** - Replace polling with filesystem watch for faster response
3. **Multi-user approvals** - Require N approvals for dangerous commands
4. **Approval history** - Query past approvals via CLI/API
5. **Response queue** - Handle multiple concurrent approval requests

### Security Considerations

1. **File locking** - State files use advisory locks (`flock`) to prevent race conditions
2. **Atomic writes** - Approval state written atomically via temp file + rename
3. **Timeout enforcement** - Prevents hung agents from blocking indefinitely
4. **Audit trail** - All approval decisions are logged immutably
5. **Opt-in by default** - Feature disabled unless explicitly enabled

### Performance Impact

- **Minimal overhead** when disabled (default)
- **Line buffering** adds ~100 bytes memory per PTY session
- **Polling** occurs only during active approval requests (100ms interval)
- **No impact** on normal PTY throughput (passthrough mode)

### Example Flow

```
1. Agent: "Allow claude to execute this Bash command? rm -rf /"
   ↓
2. Daemon detects prompt → classifies as Dangerous
   ↓
3. Writes to ~/.orkestra/approvals/ws-123_pending.json
   ↓
4. External UI reads pending request
   ↓
5. User rejects via UI
   ↓
6. UI writes to ~/.orkestra/approvals/ws-123_response.json
   ↓
7. Daemon reads response → injects "n\n"
   ↓
8. Agent receives rejection
   ↓
9. Daemon logs to audit trail
   ↓
10. Cleanup: removes pending + response files
```

## Related

- `pkg/state/approval.go` - Approval state management
- `pkg/pty/parser.go` - Permission prompt patterns
- `pkg/pty/risk.go` - Command risk classification
- `pkg/audit/audit.go` - Audit logging facility
