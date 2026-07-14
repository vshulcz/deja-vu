#!/bin/sh
# End-to-end smoke: build the real binary and exercise the user-facing paths
# against synthetic fixtures. Run from the repo root: sh scripts/e2e.sh
set -eu

fail() { echo "e2e FAIL: $*" >&2; exit 1; }

go build -o /tmp/deja-e2e ./cmd/deja

export DEJA_CLAUDE_ROOT="$PWD/fixtures/synthetic/claude"
export DEJA_CODEX_ROOT="$PWD/fixtures/synthetic/codex"
export DEJA_OPENCODE_DB="/tmp/deja-e2e-no.db"
export DEJA_INDEX_DIR="$(mktemp -d)/index.db"
export NO_COLOR=1

D=/tmp/deja-e2e

# search finds fixture content
$D frobnicator | grep -q "frobnicator" || fail "search"
# second run must be warm and quiet on stderr
out=$($D frobnicator 2>&1 >/dev/null)
[ -z "$out" ] || fail "second run not warm: $out"
# multi-word AND
$D "frobnicator bug" | grep -q "frobnicator" || fail "multi-word"
# no stray sqlite file created
[ ! -e /tmp/deja-e2e-no.db ] || fail "stray opencode.db created"
# empty result message
$D zzqqxx 2>&1 | grep -q "no matches" || fail "empty-result message"
# last + sources + version + help
$D last 5 | grep -q "claude" || fail "last"
$D sources | grep -q "claude" || fail "sources"
$D version | grep -q "deja" || fail "version"
$D | grep -q "Usage" || fail "help"
# ctx digest
$D ctx frobnicator | grep -q "deja context" || fail "ctx"
# json output is valid JSON
$D --json frobnicator | head -1 | grep -q "^\[" || fail "json"
# mcp handshake + recall
printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}\n{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"frobnicator"}}}\n' \
  | $D mcp | tail -1 | grep -q "frobnicator" || fail "mcp recall"
# install into a temp HOME is idempotent and non-destructive
H=$(mktemp -d)
mkdir -p "$H/.codex"
printf 'model = "gpt"\n' > "$H/.codex/config.toml"
HOME="$H" USERPROFILE="$H" $D install codex | grep -q "updated" || fail "install"
HOME="$H" USERPROFILE="$H" $D install codex | grep -q "unchanged" || fail "install idempotency"
grep -q 'model = "gpt"' "$H/.codex/config.toml" || fail "install clobbered config"
HOME="$H" USERPROFILE="$H" $D uninstall codex | grep -q "updated" || fail "uninstall"

echo "e2e OK"
