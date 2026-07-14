Persistent memory for your coding agents.

<!-- TODO: add demo gif here. -->

# deja-vu

`deja` indexes every session Claude Code, Codex CLI and opencode wrote on your machine, and serves it back — to you (`deja <query>`) and to your agents (MCP `recall`).

## Quickstart

```sh
go install github.com/vshulcz/deja-vu/cmd/deja@latest
deja install --all
```

Then ask your agent. It can call MCP `recall` and answer: we solved this before.

Manual CLI use still works:

```sh
deja "incremental indexing bug"
deja --harness claude --since 30d "panic|race" --re
deja ctx "database migration" > /tmp/deja-context.md
```

## Install

```sh
go install github.com/vshulcz/deja-vu/cmd/deja@latest
```

From this checkout, build the binary, then install agent integrations:

```sh
go build ./cmd/deja
./deja install --all
```

`deja install <target>` is idempotent and creates `<config>.bak` once before editing. Targets: `claude-code`, `codex`, `opencode`. Use `deja uninstall <target>` to remove the MCP entry.

## Agent memory via MCP

`deja mcp` runs a stdio MCP server. It exposes:

- `recall {query, harness?, limit?}`: dense, agent-readable matching sessions with snippets, capped under about 4KB by default for token economy.
- `recall_context {query}`: the same markdown digest as `deja ctx` for the best match.

## Harness support

| Harness | Store | Status | Install target |
| --- | --- | --- | --- |
| Claude Code | `~/.claude/projects/<project-dir>/**/*.jsonl` including `subagents/*.jsonl` | supported | `deja install claude-code` |
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` and `~/.codex/history.jsonl` | supported | `deja install codex` |
| opencode | `~/.local/share/opencode/opencode.db` | supported | `deja install opencode` |
| aider | local chat history | planned | — |
| gemini | local chat history | planned | — |

## Context pipes

`deja ctx <query|session-id-prefix>` prints a compact markdown digest for agent context: session metadata, matching user problem statements, and nearby assistant conclusions, capped around 8KB.

```sh
claude "Use this prior context: $(deja ctx 'incremental indexing bug')"
opencode run "Use this prior context: $(deja ctx c63004c3)"
```

## Performance

On a real mixed corpus (327 Claude/Codex sessions + 926 opencode sessions, about 3GB of local history), warm search is about 35ms. The index is incremental: appending to one session file updates that one source file instead of rebuilding the corpus.

## How it works

`deja` builds a local inverted index in `~/.cache/deja` and refreshes only changed source files. Claude and Codex stores are streamed from JSONL. opencode is read from the local SQLite database via the `sqlite3` command-line tool because Go stdlib has no SQLite driver.

Privacy: all local. Nothing leaves your machine. `deja` reads local history files and writes a local cache only.

Token economy: MCP `recall` is dense text under about 4KB by default; use `recall_context` only when the agent needs the fuller digest.

Environment overrides for tests or custom stores:

```sh
DEJA_CLAUDE_ROOT=/path/to/claude/projects
DEJA_CODEX_ROOT=/path/to/codex
DEJA_OPENCODE_DB=/path/to/opencode.db
DEJA_INDEX_DIR=/path/to/deja/index.db
```
