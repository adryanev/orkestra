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
| **Git auth** | Per-process GH_TOKEN injection via `gh auth token --user <profile>` — never `gh auth switch` |
| **Streaming mode** | `orkestra run --stream` outputs raw NDJSON/JSONL for live consumption |
| **Shell env capture** | Captures full user env from login shell (PATH, nvm/fnm, GOPATH) |
| **MCP server** | Stdio-based server with tools: workspace info, rename branch, notify, LSP |
| **LSP tools** | Go-to-definition, hover, references, diagnostics, rename via gopls |
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

# Create a workspace with GitHub auth profile
orkestra workspace create --repo ~/code/myproject --name fix-auth --gh-profile my-org

# Run an agent (with streaming for live NDJSON)
orkestra run --workspace <id> --prompt "Fix the auth middleware" --agent claude
orkestra run --workspace <id> --prompt "Fix it" --stream  # raw output

# Resume a session
orkestra resume --workspace <id> --prompt "Continue with tests"

# Stop an agent
orkestra stop --workspace <id>

# Todo management
orkestra todo create --title "Review PR #42" --description "Code review needed"
orkestra todo list --status todo
orkestra todo update --id <uuid> --status review
orkestra todo delete --id <uuid>

# MCP server (start alongside an agent)
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
├─ init              — Create ~/.orkestra/ + state files
├─ workspace create  — Git worktree + register (with --gh-profile)
├─ workspace list    — List all workspaces with profile info
├─ run               — Spawn agent (Claude/Codex), optional --stream
├─ resume            — Resume agent session from saved ID
├─ stop              — Kill agent process
├─ mcp               — Start stdio MCP server (LSP + workspace tools)
└─ todo create/list/update/delete
```

## Git Auth

Per-process token injection. Never calls `gh auth switch` globally.

```bash
orkestra workspace create --repo ~/code/project --name fix --gh-profile my-org
orkestra run --workspace <id> --prompt "..."  # GH_TOKEN injected automatically
```

Token resolved via: `gh auth token --user <profile>` and injected as `GH_TOKEN` env var.

## Streaming Mode

`orkestra run --stream` outputs the agent's raw NDJSON (Claude) or JSONL (Codex) directly to stdout instead of the parsed human-readable format. Hermes can consume this for live progress tracking.

## LSP Tools (via MCP)

The MCP server exposes these LSP tools for agents:

- `lsp_goto_definition` — Find symbol definition
- `lsp_hover` — Get type info and docs
- `lsp_references` — Find all references
- `lsp_diagnostics` — Get compiler errors/warnings
- `lsp_rename` — Rename symbol across workspace

Uses `gopls` under the hood, managed per-workspace.

## Comparison with Korlap

| Capability | Korlap | Orkestra |
|---|---|---|
| Platform | macOS (Tauri + Rust) | Linux/macOS (Go binary) |
| UI | Desktop app (Svelte 5) | CLI + Hermes renders output |
| Session streaming | Tauri Channel API | NDJSON/JSONL stdout parsing |
| Process control | Native Rust PTY | os/exec with goroutine readers |
| MCP server | Built-in HTTP API + register to agent | Stdio MCP server with LSP tools |
| Shell env capture | OnceLock + login shell | sync.OnceValue + login shell |
| Git auth | `gh auth token --user` | Same |
| LSP integration | Agent-side via MCP tools | Via gopls + MCP tools |
| Kanban | 4-column drag-drop UI | CLI todo commands |
| Persistence | JSON files in app data dir | JSON files in ~/.orkestra/ |

## Design Decisions

- **Go, not Rust/Tauri** — Portable single binary, same stack as Lexicon MCPs, runs on Linux VPS where Hermes lives
- **JSON files, not DB** — Simple, grep-able, human-editable
- **Stdio MCP, not HTTP** — No port allocation, works in sandboxed agent contexts
- **os/exec, not PTY** — `-p` / `exec` are pipe-friendly, no PTY overhead
- **No ACP** — ACP heredoc stdin EOF kills the protocol; `-p` / `exec` are reliable
- **Shell env from login shell** — Ensures nvm/fnm/GOPATH/etc are available

## License

MIT