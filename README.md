# Orkestra

**Orkestra** (from Indonesian *orkestrasi* — orchestration) is a CLI tool that bridges the gap between Hermes Agent's Compound Engineering workflow and full-featured agent orchestration, inspired by [Korlap](https://github.com/ariaghora/korlap).

## Concept

When Hermes delegates coding tasks to Claude Code or Codex CLI, it traditionally uses `delegate_task` — a blocking wrapper that returns after the agent finishes. Orkestra gives Hermes **structured, session-aware control** over agent processes:

```
Hermes (orchestrator)
  │
  ├─ orkestra run --workspace ws-1 --prompt "fix bug" --agent claude
  │     ↓
  │   Spawns Claude -p --output-format stream-json --verbose
  │   Parses NDJSON live: session_id, text, usage, tool calls
  │   Saves session → ~/.orkestra/sessions.json
  │
  ├─ orkestra resume --workspace ws-1 --prompt "lanjut"
  │     ↓
  │   Reads saved session_id → Claude --resume <id>
  │
  ├─ orkestra stop --workspace ws-1
  │     ↓
  │   Kills agent process
  │
  └─ orkestra todo create --title "review PR"
       ↓
     Kanban-style task management
```

## Features

| Feature | Description |
|---|---|
| **Dual agent support** | Claude Code (`-p --output-format stream-json`) and Codex (`exec --json`) |
| **Session lifecycle** | Capture session_id/thread_id, save, resume later |
| **Structured output** | Parse NDJSON (Claude) and JSONL (Codex) for text, usage, tool calls |
| **Git worktree isolation** | Workspaces live in app data dir, zero files in managed repo |
| **Git auth** | Per-process GH_TOKEN injection — never `gh auth switch` |
| **MCP server** | Stdio-based server with tools: get_workspace_info, rename_branch, notify |
| **Todo management** | Kanban-style CRUD with JSON persistence |
| **Process management** | Start, monitor, kill agent processes |

## Installation

```bash
git clone https://github.com/adryanev/orkestra
cd orkestra
go build -o ~/.local/bin/orkestra ./main.go
```

## Usage

```bash
# Initialize
orkestra init

# Create a workspace
orkestra workspace create --repo ~/code/myproject --name fix-auth

# Run an agent
orkestra run --workspace <id> --prompt "Fix the auth middleware" --agent claude

# Resume a session
orkestra resume --workspace <id> --prompt "Continue with tests"

# Stop an agent
orkestra stop --workspace <id>

# Todo management
orkestra todo create --title "Review PR #42" --description "Code review needed"
orkestra todo list --status todo
orkestra todo update --id <uuid> --status review
orkestra todo delete --id <uuid>

# MCP server
orkestra mcp
```

## Architecture

```
~/.orkestra/
  workspaces.json    — workspace registry
  sessions.json      — session_id/thread_id per workspace
  todos.json         — kanban task list
  worktrees/<id>/    — git worktrees (cloned repos)
  mcp/               — MCP config files

orkestra             — CLI entrypoint
├─ init              — Create ~/.orkestra/
├─ workspace create  — Git worktree + register
├─ workspace list    — List all workspaces
├─ run               — Spawn agent (Claude/Codex)
├─ resume            — Resume agent session
├─ stop              — Kill agent process
├─ mcp               — Start stdio MCP server
└─ todo create/list/update/delete
```

## Comparison with Korlap

| Capability | Korlap | Orkestra |
|---|---|---|
| Platform | macOS (Tauri + Rust) | Linux/macOS (Go binary) |
| UI | Desktop app (Svelte 5) | CLI + Hermes renders output |
| Session streaming | Tauri Channel API | NDJSON/JSONL stdout parsing |
| Process control | Native Rust PTY | os/exec with goroutine readers |
| MCP server | Built-in HTTP API + register to agent | Stdio MCP server |
| Shell env capture | OnceLock + login shell | Inherits Hermes env |
| Git auth | `gh auth token --user` | Same |
| Kanban | 4-column drag-drop UI | CLI todo commands |
| Persistence | JSON files in app data dir | JSON files in ~/.orkestra/ |
| LSP | Agent-side via MCP tools | Not yet |

## Design Decisions

- **Go, not Rust/Tauri** — Portable single binary, same stack as Lexicon MCPs, runs on Linux VPS where Hermes lives
- **JSON files, not DB** — Simple, grep-able, human-editable
- **Stdio MCP, not HTTP** — No port allocation, works in sandboxed agent contexts
- **os/exec, not PTY** — `-p` / `exec` are pipe-friendly, no PTY overhead
- **No ACP** — ACP heredoc stdin EOF kills the protocol; `-p` / `exec` are reliable

## License

MIT