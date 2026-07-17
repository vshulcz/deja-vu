# Cursor session format

## Stores and files

Cursor IDE stores chats in `state.vscdb` under `globalStorage/` and `workspaceStorage/*/`. The user root is `~/Library/Application Support/Cursor/User` on macOS and `~/.config/Cursor/User` on other systems; an existing `$XDG_CONFIG_HOME/Cursor/User` is used when available. `DEJA_CURSOR_ROOT` overrides IDE discovery.

Cursor CLI writes `projects/<encoded-path>/agent-transcripts/**/*.jsonl` below `${CURSOR_CONFIG_DIR:-~/.cursor}`. `DEJA_CURSOR_CLI_ROOT` overrides transcript reads. Subagent transcripts are excluded unless `DEJA_INCLUDE_SUBAGENTS=1`.

## IDE SQLite records

The `cursorDiskKV(key, value)` table has session values at `composerData:<id>` and message values at `bubbleId:<composer-id>:<bubble-id>`.

```json
{"composerId":"composer-7","name":"Fix cache invalidation","createdAt":1784278800000,"lastUpdatedAt":1784278802000}
{"type":1,"text":"Why is this stale?","timestamp":1784278801000,"workspaceProjectDir":"/work/api"}
```

Bubble `type: 1` maps to `user`; other numeric types map to `assistant`. Text uses `text`, falling back to `rawText`. Timestamps are Unix milliseconds. The first bubble with `workspaceProjectDir` supplies the project. Reading SQLite requires the `sqlite3` CLI.

## CLI JSONL records

```json
{"role":"assistant","message":{"content":[{"type":"text","text":"The cache key omitted the locale."}]}}
```

Only `user` and `assistant` roles are retained. Content follows the Anthropic string-or-parts shape. Control records such as `turn_ended` are ignored. The transcript has no message timestamps, so deja uses file modification time.

## Known quirks and drift

- Cursor moved modern IDE chats toward global storage while older versions used workspace databases; both are scanned.
- SQLite values are JSON inside a key-value table and malformed or null entries occur.
- CLI project path encoding has the same separator-versus-hyphen ambiguity as Claude Code.
- SQLite text is capped at 64 KiB. CLI subagent duplication is opt-in.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
