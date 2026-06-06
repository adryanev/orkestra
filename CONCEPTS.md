# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary only, not a spec or catch-all.

## Agent Orchestration

### Workspace
A registered isolated working copy where an agent can operate on a repository branch while Orkestra tracks its path, branch, status, and related session state.

### Agent Run
The lifecycle of starting a supported coding agent inside a Workspace, wiring workspace-aware tools into that process, streaming or parsing the agent output, and persisting the resulting Agent Session.

### Agent Session
The saved continuation identity for an Agent Run; Orkestra stores the agent-specific conversation identifier so a later resume command continues the same conversation instead of starting fresh.

### Process Identity
The operating-system proof that a persisted process identifier still belongs to the agent process Orkestra started.

Process Identity combines the process group with a start token so stop and cleanup commands do not signal an unrelated recycled process.

### Bound MCP Server
An Orkestra MCP server instance tied to exactly one Workspace for an Agent Run.

Bound MCP Server tools resolve file paths and workspace operations against their bound Workspace rather than accepting arbitrary workspace context from the agent.

### LSP Pool
The set of language-server processes Orkestra starts and reuses for a Workspace so MCP LSP tools can share initialized servers while keeping startup and request handling bounded.
