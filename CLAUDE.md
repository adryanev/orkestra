# Orkestra — Hermes Agent Orchestrator

CLI tool + MCP server that bridges the gaps between Hermes CE workflow and full-featured agent orchestration (inspired by Korlap).

## Stack

- Go 1.22+ (single binary, no runtime deps)
- Cobra + Viper for CLI/config
- git worktree for workspace isolation
- stdlib `os/exec` for process management
- JSON file persistence (no DB)

## Architecture

```
~/.orkestra/
  workspaces.json        — workspace registry
  sessions.json          — session_id / thread_id per workspace
  worktrees/<id>/        — git worktrees (cloned repos)
  mcp/<ws-id>.json       — MCP config files injected to agents

orkestra                — CLI entrypoint
orkestra mcp            — MCP server (stdio mode), agent calls back
```

## What to build (Phase 1 MVP)

### 1. CLI skeleton
- Cobra root command
- Viper config (XORKESTRA_HOME env var, default ~/.orkestra/)
- Subcommands: workspace, run, resume, stop, mcp, todo, init

### 2. `orkestra init`
- Create ~/.orkestra/ dir structure
- Create empty workspaces.json, sessions.json

### 3. `orkestra workspace create`
- Args: --repo <path> --name <name> --branch <branch> (optional)
- Creates git worktree from origin/<default-branch>
- Registers in workspaces.json with UUID, worktree_path, branch, status
- Auto-detect default branch (origin/HEAD -> origin/main -> origin/master)

### 4. `orkestra run`
- Args: --workspace <id> --prompt <string> (or stdin) --agent claude|codex
- **Claude mode**: `claude -p --output-format stream-json --verbose --permission-mode bypassPermissions --disallowedTools "EnterWorktree,ExitWorktree"`
- Stream NDJSON stdout line by line, print readable output
- Capture `session_id` from `{"type":"system","session_id":"..."}` first line
- Save session_id to sessions.json
- On exit: print usage report from `{"type":"result","usage":{...}}`

### 5. `orkestra resume`
- Args: --workspace <id> --prompt <string>
- Read session_id from sessions.json
- Spawn Claude with `--resume <session_id>` + new prompt
- Same streaming + capture behavior as `run`

### 6. `orkestra stop`
- Args: --workspace <id>
- Kill the running agent process for that workspace
- Clean up process tracking state

### 7. `orkestra mcp` (MCP server)
- Stdio MCP server using stdio transport
- Runs on stdin/stdout, reads JSON-RPC messages
- Implements these tools:
  - `get_workspace_info` — return workspace path, branch, status
  - `rename_branch` — rename git branch + update workspace state
  - `notify` — write notification to a per-workspace log file
- NOT a running HTTP server — just stdio mode (like Claude's MCP)

### 8. Process management
- Track child PIDs per workspace in-memory + persisted
- Handle graceful and forceful process termination
- Stdin/stdout/stderr pipe management

### 9. Git auth
- Inject GH_TOKEN from `gh auth token --user <profile>` when available
- --gh-profile flag per workspace

### 10. `orkestra todo`
- create/list/update/delete — JSON file persistence
- NOT a kanban UI, just CLI commands

### 11. `orkestra workspace list`
- List all workspaces with status, branch, name

## Hard rules

- Every CLI command returns proper exit codes (0=ok, 1=error)
- All errors to stderr, all results to stdout
- No panics in production paths — always return errors
- Async filesystem ops must have timeout where applicable
- JSON output mode: add --json flag to all commands for structured output
- Never use ACP protocol — always `-p` / `exec` for agent CLIs
- Never call `gh auth switch` — inject token as env var per process

## Design decisions

- **Why not Tauri/Rust?** Hermes runs on Linux VPS, not macOS. Go binary is portable, zero native deps, same language as Lexicon MCPs.
- **State**: JSON files, not DB. Simpler, grep-able, human-editable for debugging.
- **MCP**: stdio mode only. The orchestrator spawns the MCP server alongside the agent, agent calls back via Korlap MCP tool.
- **Session IDs**: Claude uses `session_id`, Codex uses `thread_id`. Both saved in sessions.json as `{workspace_id: {agent: "claude", id: "..."}}`.