# Orkestra — Agent Orchestration CLI

CLI tool + MCP server for managing isolated agent workspaces, running Claude Code or Codex, and exposing workspace-aware MCP/LSP tools.

## Stack

- Go 1.26+ (single binary, no runtime deps)
- Cobra + Viper for CLI/config
- git worktree for workspace isolation
- stdlib `os/exec` for process management
- JSON file persistence (no DB)

## Architecture

```
~/.orkestra/
  workspaces.json        — workspace registry
  sessions.json          — session_id / thread_id per workspace
  worktrees/<id>/        — git worktrees (linked working dirs sharing the repo object DB, not separate clones)
  mcp/<ws-id>.json       — MCP config files injected to agents

orkestra                — CLI entrypoint
orkestra mcp            — MCP server (stdio mode), agent calls back
```

## Project Knowledge

- `docs/solutions/` — documented solutions to past problems (bugs, best practices, workflow patterns), organized by category with YAML frontmatter (`module`, `tags`, `problem_type`). Relevant when implementing or debugging in documented areas.
- `CONCEPTS.md` — shared domain vocabulary for project-specific entities, named processes, and status concepts. Relevant when orienting to the codebase or discussing Orkestra domain concepts.

## Commands

| Command | Purpose |
|---------|---------|
| `orkestra init` | Creates the Orkestra state directory and initial JSON files |
| `orkestra workspace create` | Creates a git worktree and registers it as a workspace |
| `orkestra workspace list` | Lists registered workspaces |
| `orkestra workspace remove` | Removes a workspace and its git worktree |
| `orkestra run` | Starts Claude Code or Codex in a workspace |
| `orkestra resume` | Continues the saved session for a workspace |
| `orkestra stop` | Stops the persisted agent process for a workspace |
| `orkestra todo ...` | Creates, lists, updates, and deletes todos |
| `orkestra mcp --workspace <id>` | Starts the stdio MCP server for one workspace |

## Hard rules

- Every CLI command returns proper exit codes (0=ok, 1=error)
- All errors to stderr, all results to stdout
- No panics in production paths — always return errors
- Async filesystem ops must have timeout where applicable
- JSON output mode: add --json flag to all commands for structured output
- Never use ACP protocol — always `-p` / `exec` for agent CLIs
- Never call `gh auth switch` — inject token as env var per process

## Design decisions

- **Why not Tauri/Rust?** Single Go binary is portable, zero native deps, same language as MCP ecosystem.
- **State**: JSON files, not DB. Simpler, grep-able, human-editable for debugging.
- **MCP**: stdio mode only. The orchestrator spawns the MCP server alongside the agent, and the agent calls back through workspace-bound tools.
- **Session IDs**: Claude uses `session_id`, Codex uses `thread_id`. Both saved in sessions.json as `{workspace_id: {agent: "claude", id: "..."}}`.