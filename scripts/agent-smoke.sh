#!/bin/sh
# Manual, on-demand smoke: a REAL agent (qwen-code) calls deja's recall tool over
# MCP, backed by a free OpenRouter model. Proves the end-to-end agent<->MCP loop
# that unit tests can't. NOT wired into CI — free models are flaky and the agent
# CLI is third-party, so this is a human-run check, not a merge gate.
#
# Requires: OPENROUTER_API_KEY, and qwen-code on PATH (npm i -g @qwen-code/qwen-code).
# Usage: OPENROUTER_API_KEY=sk-or-... sh scripts/agent-smoke.sh
set -eu

if [ -z "${OPENROUTER_API_KEY:-}" ]; then
	echo "set OPENROUTER_API_KEY (a free key is fine)" >&2
	exit 2
fi
if ! command -v qwen >/dev/null 2>&1; then
	echo "qwen not found: npm i -g @qwen-code/qwen-code" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

deja="$work/deja"
go build -o "$deja" "$root/cmd/deja"

# Hermetic HOME with a single seeded session holding a marker the model can only
# produce by actually calling recall.
marker="quokkasprocket"
home="$work/home"
claude="$work/claude"
proj="$claude/-smoke"
mkdir -p "$proj" "$home/.qwen"
cat > "$proj/s1.jsonl" <<EOF
{"type":"user","sessionId":"smoke-1","cwd":"/tmp/smoke","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the $marker deadlock in scheduler.go"}}
{"type":"assistant","sessionId":"smoke-1","cwd":"/tmp/smoke","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":"fixed the $marker deadlock"}}
EOF

cat > "$home/.qwen/settings.json" <<EOF
{
  "mcpServers": { "deja": { "command": "$deja", "args": ["mcp"] } },
  "security": { "auth": { "selectedType": "openai" } },
  "ui": { "autoModeAcknowledged": true }
}
EOF

export HOME="$home"
export DEJA_CLAUDE_ROOT="$claude"
export DEJA_INDEX_DIR="$work/index.db"
export OPENAI_API_KEY="$OPENROUTER_API_KEY"
export OPENAI_BASE_URL="https://openrouter.ai/api/v1"
export QWEN_CODE_SUPPRESS_YOLO_WARNING=1

prompt="Call the MCP tool named recall with query \"$marker\". Then print the first session title verbatim."

# Free models come and go and vary in tool-call reliability; try a few.
for model in \
	"nvidia/nemotron-3-super-120b-a12b:free" \
	"openai/gpt-oss-20b:free" \
	"nvidia/nemotron-3-ultra-550b-a55b:free"; do
	echo "--- trying $model ---"
	out=$(qwen --yolo -m "$model" -p "$prompt" 2>/dev/null || true)
	printf '%s\n' "$out"
	if printf '%s' "$out" | grep -q "$marker"; then
		echo "PASS: agent called recall and got the seeded session ($model)"
		exit 0
	fi
done

echo "FAIL: no free model returned the seeded marker; recall not proven this run" >&2
exit 1
