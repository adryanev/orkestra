---
title: "Single-flight initialization for a pooled, cached-by-key resource"
date: 2026-06-07
category: design-patterns
module: orkestra-mcp-lsp
problem_type: design_pattern
component: tooling
severity: medium
applies_when:
  - "A resource is lazily created and cached by key, and the create step is slow (spawns a process, does I/O, runs a handshake)"
  - "Concurrent callers may request the same key at once, or different keys that should not block each other"
  - "Holding one global lock across the slow create step would serialize unrelated work"
root_cause: scope_issue
resolution_type: code_fix
related_components:
  - assistant
tags:
  - concurrency
  - single-flight
  - resource-pooling
  - lazy-initialization
  - mutex-contention
  - go
---

# Single-flight initialization for a pooled, cached-by-key resource

## Context

`LspPool` owns the language servers for a single workspace, keyed by server id (`servers map[string]*lspServer`). MCP requests funnel through `serverForFile` → `getOrStart`, and several can arrive at once. Starting a server is slow: `startServer` spawns a child process and runs the LSP `initialize`/`initialized` handshake (`pkg/mcp/lsp.go`).

Before the fix, `getOrStart` held the pool mutex across the entire start path with `defer p.mu.Unlock()`:

```go
func (p *LspPool) getOrStart(cfg LspServerConfig) (*lspServer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.servers[cfg.ServerID]; ok && s.alive() {
		return s, nil
	}
	if env.LookPath(cfg.Command) == "" {
		return nil, fmt.Errorf("%s", cfg.InstallHint)
	}
	s, err := startServer(p.root, cfg) // slow: spawn + handshake, under the global lock
	if err != nil {
		return nil, err
	}
	p.servers[cfg.ServerID] = s
	return s, nil
}
```

This holds correctness (one server per id) but creates two problems (session history): the pool mutex is a single global lock for all server ids, so a slow `startServer` for `gopls` blocks an unrelated caller wanting `typescript-language-server`; and naively releasing the lock around the start would let two concurrent callers for the *same* id each spawn a server.

## Guidance

Keep a separate `starting` map of in-flight starts keyed by server id, each carrying a `done` channel and the shared result. The first caller claims the slot and releases the mutex before the slow work; later callers find the in-flight entry, release the mutex, wait on `done`, then read the shared result.

```go
type serverStart struct {
	done   chan struct{}
	server *lspServer
	err    error
}
```

`getOrStart` makes a three-way decision under the lock and releases it before any slow work (`pkg/mcp/lsp_pool.go`):

```go
func (p *LspPool) getOrStart(cfg LspServerConfig) (*lspServer, error) {
	p.mu.Lock()
	if s, ok := p.servers[cfg.ServerID]; ok {
		if s.alive() {
			p.mu.Unlock()
			return s, nil
		}
		delete(p.servers, cfg.ServerID) // reap a dead server before restarting
	}
	if start, ok := p.starting[cfg.ServerID]; ok { // someone is already starting this id
		p.mu.Unlock()
		<-start.done
		if start.err != nil {
			return nil, start.err
		}
		if start.server != nil && start.server.alive() {
			return start.server, nil
		}
		return p.getOrStart(cfg) // it died meanwhile; retry
	}

	start := &serverStart{done: make(chan struct{})}
	p.starting[cfg.ServerID] = start
	p.mu.Unlock() // release before the slow start

	if env.LookPath(cfg.Command) == "" {
		start.err = fmt.Errorf("%s", cfg.InstallHint)
		p.finishStart(cfg.ServerID, start)
		return nil, start.err
	}
	s, err := startServer(p.root, cfg) // runs UNLOCKED
	start.server, start.err = s, err
	if err != nil {
		p.finishStart(cfg.ServerID, start)
		return nil, err
	}
	p.mu.Lock()
	if existing, ok := p.servers[cfg.ServerID]; ok && existing.alive() {
		start.server = existing // a live server appeared; adopt it and close ours
		s.Close()
	} else {
		p.servers[cfg.ServerID] = s
	}
	delete(p.starting, cfg.ServerID)
	close(start.done)
	p.mu.Unlock()
	return start.server, nil
}
```

All failure exits funnel through one cleanup helper so the in-flight slot is always cleared and waiters are always woken — no caller blocks forever on `done`:

```go
func (p *LspPool) finishStart(serverID string, start *serverStart) {
	p.mu.Lock()
	delete(p.starting, serverID)
	close(start.done)
	p.mu.Unlock()
}
```

Two details that keep it correct: a **double-check on completion** (if another live server appeared while we were starting, adopt it and `Close()` the redundant one) and **dead-server reaping** (delete a cached-but-dead server before restarting).

## Why This Matters

Without single-flight, a burst of requests for the same server spawns duplicate processes (resource waste, and ambiguity over which handle is canonical). Holding the global lock across the slow start also starves callers for *unrelated* servers — a `gopls` cold start blocking a `typescript-language-server` lookup. The pattern preserves the "exactly one live server per id" invariant while letting unrelated starts proceed concurrently.

## When to Apply

- Any lazily-initialized, expensive, cached-by-key resource accessed concurrently: connection pools, compiled caches, server handles, sandboxes, headless browsers — any "get-or-create" whose create step does I/O or spawns processes.
- Go's `golang.org/x/sync/singleflight` solves the same shape; a hand-rolled version is worth it when you also need per-key caching, dead-entry reaping, and adopt-and-close on completion, as here.

A `sync.Once` per entry was considered and rejected: keying it at pool level re-creates the mutex-hold problem, and it does not express "different keys proceed, same key waits" as cleanly. (session history)

## Examples

Before — global mutex held across `startServer` (see Context). After — per-server in-flight record, `startServer` runs unlocked, the lock is taken and released once per branch (see Guidance).

## Prevention

Test coverage (`pkg/mcp/lsp_test.go`):

- `TestMissingBinaryReturnsInstallHint` (`:262`) drives `serverForFile` → `getOrStart` down the `env.LookPath == ""` branch, exercising `finishStart` cleanup.
- `TestCallTimeoutMarksServerUnhealthy` (`:104`) makes a timed-out server report `!alive()`, the precondition for reaping.
- `TestGoplsHoverIntegration` (`:390`) starts a real server through `getOrStart`.

Adjacent reliability fix in the same layer (not single-flight, but worth knowing): the LSP reader/writer was deadlocking when a reply took the write lock inside the reader goroutine; a dedicated writer goroutine with a non-blocking outbound channel fixed it, guarded by `TestReaderNotBlockedByStuckWriter`. (session history)

## Related

- Incident record this pattern was extracted from: [Real Binary Agent Process Hardening](../integration-issues/real-binary-agent-process-hardening.md)
- `CONCEPTS.md` → **LSP Pool**
