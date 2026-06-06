---
title: "Orkestra Phase 2: Korlap Parity Features"
date: 2026-02-26
category: build-config
tags: [orkestra, korlap, parity, shell-env, git-auth, streaming, lsp]
module: orkestra
component: orkestra-parity
problem_type: feature
resolution_type: implementation
---

# Orkestra Phase 2: Korlap Parity Features

## Context

Orkestra is a Go CLI tool + MCP server that bridges gaps between Hermes CE workflow and full-featured agent orchestration, inspired by Korlap (Tauri/Rust). The MVP (Phase 1) had basic workspace management, agent invocation, and session tracking. Phase 2 closed four parity gaps with Korlap.

### What was missing

1. **Shell environment capture** — spawned agents (Claude Code, Codex) inherited Hermes' bare env, losing PATH, GOROOT, nvm/fnm node bins, and other login-shell context. Agents couldn't find tools or compile.
2. **Git auth per-process** — used global `gh auth switch` which changed the user's global auth state. No per-workspace profile support.
3. **Streaming output** — blocked on agent completion before showing any output. No way to see real-time progress.
4. **LSP MCP tools** — no IDE-like capabilities (go-to-definition, hover, diagnostics) for the orchestrator to inspect code during review.

## Guidance

### Shell Environment Capture

**`pkg/env/capture.go`** — capture login shell env deterministically:

```go
func Capture() (map[string]string, error) {
    cmd := exec.Command("zsh", "-lic", "/usr/bin/env")
    cmd.Env = []string{
        "SHELL=" + shellPath,
        "HOME=" + os.Getenv("HOME"),
        "TERM=" + os.Getenv("TERM"),
    }
    out, err := cmd.Output()
    // parse key=value lines into map
}
```

- Uses `sync.OnceValue` to cache — called once, reused across all agent spawns.
- PATH, GOROOT, GOPATH, fnm/nvm paths all come through.
- Falls back to `os.Environ()` if capture fails.

### Git Auth Per-Process

**`pkg/gitauth/auth.go`** — resolve GH_TOKEN per profile:

```go
func ResolveToken(profile string) (string, error) {
    cmd := exec.Command("gh", "auth", "token", "--user", profile)
    out, err := cmd.Output()
    return strings.TrimSpace(string(out)), err
}
```

- `GH_TOKEN` injected into agent process env — never calls `gh auth switch`.
- `GhProfile` field on Workspace struct, set via `--gh-profile` flag.
- Defaults to empty (no token injection) when omitted.

### Streaming Output

**Modified `cmd/run.go`** — `--stream` flag changes output mode:

- Without `--stream`: collect all stdout, print when process exits (default).
- With `--stream`: pipe stdout line-by-line, print each line as it arrives.
- Raw NDJSON passthrough — orchestrator parses agent progress events.
- Uses `bufio.Scanner` on the process stdout pipe for real-time reads.

### LSP MCP Tools

**`pkg/mcp/lsp.go`** — gopls integration via JSON-RPC:

- Spawns `gopls serve` per workspace.
- Manages JSON-RPC communication (Content-Length headers).
- Five tools registered: `goto_definition`, `hover`, `references`, `diagnostics`, `rename`.
- Tools connect to the running gopls instance for the requested workspace.

## File Changes

| File | Change |
|---|---|
| `pkg/env/capture.go` | New — shell env capture with sync.OnceValue |
| `pkg/gitauth/auth.go` | New — gh auth token resolver |
| `pkg/workspace/workspace.go` | Added `GhProfile` field |
| `cmd/run.go` | `--stream` flag, env injection, shell env integration |
| `cmd/workspace.go` | `--gh-profile` flag |
| `pkg/mcp/lsp.go` | New — gopls JSON-RPC integration |
| `pkg/mcp/server.go` | Register LSP tools |
| `README.md` | Updated with new features |

## Why This Matters

Without these features, agents spawned by Orkestra:

- Can't find installed tools (wrong PATH)
- Use wrong Git identity (global `gh auth switch` contaminates other worktrees)
- Force the orchestrator to wait silently until complete
- Can't perform code inspection beyond grep

With Phase 2, Orkestra reaches functional parity with Korlap's agent orchestration capabilities, making it production-ready for Hermes compound engineering workflows.

## When to Apply

- When spawning Claude Code or Codex via `orkestra run` and they fail to find installed binaries
- When working with multiple GitHub profiles across workspaces
- When monitoring long-running agent tasks
- When the orchestrator needs code inspection during review