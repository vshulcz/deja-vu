# Claude Code session format

## Store and files

Claude Code writes transcripts below `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/`. deja can point only session reads elsewhere with `DEJA_CLAUDE_ROOT`. Files are JSONL and normally named for a session ID. A project directory encodes an absolute path by replacing both separators and literal hyphens with `-`; nested `subagents/*.jsonl` files are excluded unless `DEJA_INCLUDE_SUBAGENTS=1`.

The top-level `~/.claude.json` is not a transcript. When `CLAUDE_CONFIG_DIR` is set, Claude moves that file to `$CLAUDE_CONFIG_DIR/.claude.json`.

## Records

Message records have `type`, `sessionId`, `timestamp`, and a `message` object. `message.content` is either a string or an array whose useful parts have `text` or `content`.

```json
{"type":"assistant","sessionId":"session-7","timestamp":"2026-07-17T09:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"The failing check is in parser.go."}]}}
```

`type: "user"` maps to `user` and `type: "assistant"` maps to `assistant`; a non-empty `message.role` takes precedence. Timestamps are RFC 3339 strings in observed files. deja also accepts numeric Unix seconds or milliseconds.

## Known quirks and drift

- JSONL can end in a partial line while Claude is writing. Malformed lines are skipped; a later indexing pass reads the completed tail.
- Tool and control records use other `type` values and do not become messages.
- Project path encoding is ambiguous because `-` represents both a separator and a hyphen. deja checks the local filesystem before using a two-segment fallback.
- Subagent logs largely duplicate parent content and are opt-in.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
