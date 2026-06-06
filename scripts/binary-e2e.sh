#!/usr/bin/env bash
set -euo pipefail

BIN="${1:-./orkestra}"
if [[ ! -x "$BIN" ]]; then
  echo "binary not executable: $BIN" >&2
  exit 1
fi

ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

ORK_HOME="$ROOT/orkestra-home"
REPO="$ROOT/repo"
ORIGIN="$ROOT/origin.git"
mkdir -p "$ORK_HOME" "$REPO"

json_get() {
  local key="$1"
  python3 -c 'import json,sys
obj=json.load(sys.stdin)
for part in sys.argv[1].split("."):
    obj=obj[int(part)] if isinstance(obj, list) else obj[part]
print(obj)
' "$key"
}

json_len() {
  python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'
}

assert_fail() {
  if "$@" >"$ROOT/unexpected.out" 2>"$ROOT/expected.err"; then
    echo "expected failure, got success: $*" >&2
    cat "$ROOT/unexpected.out" >&2
    exit 1
  fi
}

git -C "$REPO" init -q
git -C "$REPO" config user.email "orkestra-e2e@example.com"
git -C "$REPO" config user.name "Orkestra E2E"
printf 'hello\n' >"$REPO/README.md"
git -C "$REPO" add README.md
git -C "$REPO" commit -q -m init
git -C "$REPO" branch -M main
git init --bare -q "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q -u origin main
git -C "$REPO" remote set-head origin -a >/dev/null

command -v claude >/dev/null || {
  echo "real claude binary is required for binary e2e" >&2
  exit 1
}

export XORKESTRA_HOME="$ORK_HOME"
export SHELL="/bin/sh"

"$BIN" init >/dev/null
test -f "$ORK_HOME/workspaces.json"
test -f "$ORK_HOME/sessions.json"
test -f "$ORK_HOME/todos.json"

# Positive: create/list workspace using real git worktree and JSON output.
create_out="$("$BIN" --json workspace create --repo "$REPO" --name positive --branch test/positive)"
WS_ID="$(printf '%s' "$create_out" | json_get id)"
WS_PATH="$(printf '%s' "$create_out" | json_get worktree_path)"
test -d "$WS_PATH/.git" || test -f "$WS_PATH/.git"

list_out="$("$BIN" --json workspace list)"
list_len="$(printf '%s' "$list_out" | json_len)"
[[ "$list_len" -ge 1 ]]

# Positive: run with the real Claude Code binary. JSON stdout must stay
# parseable and must not be contaminated by live assistant text.
run_out="$("$BIN" --json run --workspace "$WS_ID" --agent claude --prompt "Reply exactly: ORKESTRA_BINARY_E2E_POSITIVE")"
session_id="$(printf '%s' "$run_out" | json_get session_id)"
[[ -n "$session_id" ]]
if [[ "$run_out" == *"ORKESTRA_BINARY_E2E_POSITIVE"* ]]; then
  echo "run --json stdout was contaminated by agent text" >&2
  exit 1
fi

# Positive: raw stream mode passes through real agent NDJSON.
stream_out="$("$BIN" run --workspace "$WS_ID" --agent claude --prompt "Reply exactly: ORKESTRA_BINARY_E2E_STREAM" --stream)"
[[ "$stream_out" == *'"type":"system"'* ]]
[[ "$stream_out" == *'"type":"result"'* ]]

# Positive: resume same-agent saved session.
resume_out="$("$BIN" --json resume --workspace "$WS_ID" --agent claude --prompt "Reply exactly: ORKESTRA_BINARY_E2E_RESUME")"
resume_session="$(printf '%s' "$resume_out" | json_get session_id)"
[[ -n "$resume_session" ]]

# Positive: todo lifecycle.
todo_out="$("$BIN" --json todo create --title "Binary test" --workspace "$WS_ID")"
TODO_ID="$(printf '%s' "$todo_out" | json_get id)"
"$BIN" --json todo update --id "$TODO_ID" --status done >/dev/null
done_len="$("$BIN" --json todo list --status done | json_len)"
[[ "$done_len" -eq 1 ]]
"$BIN" --json todo delete --id "$TODO_ID" >/dev/null

# Negative: missing inputs / invalid state fail.
assert_fail "$BIN" workspace create --repo "$ROOT/missing" --name bad
assert_fail "$BIN" run --workspace missing --agent claude --prompt "no workspace"
assert_fail "$BIN" mcp
assert_fail "$BIN" resume --workspace "$WS_ID" --agent codex --prompt "wrong agent"

# Edge: odd workspace names are slugged, and dirty worktrees require --force.
edge_out="$("$BIN" --json workspace create --repo "$REPO" --name "Edge Case !!")"
EDGE_ID="$(printf '%s' "$edge_out" | json_get id)"
EDGE_PATH="$(printf '%s' "$edge_out" | json_get worktree_path)"
printf 'dirty\n' >"$EDGE_PATH/dirty.txt"
assert_fail "$BIN" workspace remove --id "$EDGE_ID"
"$BIN" workspace remove --id "$EDGE_ID" --force >/dev/null
test ! -e "$EDGE_PATH"

# Edge: stop is idempotent after the agent has already exited.
"$BIN" stop --workspace "$WS_ID" >/dev/null
"$BIN" stop --workspace "$WS_ID" >/dev/null

echo "binary e2e passed"
