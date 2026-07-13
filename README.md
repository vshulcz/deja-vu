Your agents already solved this. deja finds it.

# deja-vu

`deja` is a fast Go CLI for searching AI coding-agent session histories across Claude Code, Codex CLI, and opencode.

![demo gif placeholder](docs/demo.gif)

## Install

```sh
go install github.com/vshulcz/deja-vu/cmd/deja@latest
```

Or from this checkout:

```sh
go build ./cmd/deja
./deja sources
```

## Examples

```sh
deja frobnicator
deja --re 'panic|race' --harness claude --since 30d
deja --json "database migration"
deja --project deja-vu --role user parser
deja ctx "database migration" > context.md
claude "context:$(deja ctx c63004c3)"
deja show c63004c3
deja last 20
deja sources
```

Search defaults to case-insensitive substring matching. Results are grouped by session, ranked by match count and recency, with up to three highlighted snippets per session. `NO_COLOR=1` disables ANSI highlighting.

`deja ctx <query|session-id-prefix>` prints a compact markdown digest for agent context: session metadata, matching user problem statements, and nearby assistant conclusions, capped around 8KB. It writes plain markdown to stdout, so it can be piped into Claude Code or opencode:

```sh
claude "context:$(deja ctx 'incremental indexing bug')"
deja ctx c63004c3 > /tmp/deja-context.md
opencode run "Use this prior context: $(deja ctx c63004c3)"
```

## Harnesses

| Harness | Store | Status |
| --- | --- | --- |
| Claude Code | `~/.claude/projects/<project-dir>/**/*.jsonl` including `subagents/*.jsonl` | user/assistant messages |
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` and `~/.codex/history.jsonl` | rollout + prompt history |
| opencode | `~/.local/share/opencode/opencode.db` | sessions, messages, text parts |

## Notes

`deja` is stdlib-only Go. Claude and Codex stores are streamed from JSONL. opencode currently reads the local SQLite database through the `sqlite3` command-line tool, because Go stdlib has no SQLite driver.

Environment overrides for tests or custom stores:

```sh
DEJA_CLAUDE_ROOT=/path/to/claude/projects
DEJA_CODEX_ROOT=/path/to/codex
DEJA_OPENCODE_DB=/path/to/opencode.db
```
