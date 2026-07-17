# Qwen Code session format

## Store and files

Qwen Code writes sessions below `${DEJA_QWEN_ROOT:-~/.qwen}/projects/<encoded-project>/chats/*.jsonl`. The project directory uses the same slash-and-hyphen encoding as Claude Code. The `chats/` directory is part of Qwen's layout; only JSONL files directly inside it are session streams.

The Qwen configuration directory remains `~/.qwen` for installer settings. `DEJA_QWEN_ROOT` relocates session reads only.

## Records

Each line is a JSON object with `type`, `sessionId`, `timestamp`, and a `message` object. Message text is in `message.parts`; parts marked `thought: true` are reasoning and are excluded.

```json
{"type":"assistant","sessionId":"session-7","timestamp":"2026-07-17T09:00:01Z","message":{"role":"model","parts":[{"text":"The failing check is in parser.go."}]}}
```

`message.role` `model` maps to `assistant` and `user` maps to `user`; when it is absent, the top-level `type` is used. Parts with text are joined with newlines. RFC 3339 and numeric Unix timestamps are accepted.

## Known quirks and drift

- JSONL can end in a partial line while Qwen is writing. Malformed lines are skipped.
- System, tool-result, and other control records do not become messages.
- Project path encoding is ambiguous because `-` represents both a separator and a hyphen. deja checks the local filesystem before using a two-segment fallback.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
