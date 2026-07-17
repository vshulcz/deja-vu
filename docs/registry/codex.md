# Codex CLI session format

## Store and files

Codex stores state under `${CODEX_HOME:-~/.codex}`. deja's read-only override is `DEJA_CODEX_ROOT`. Rollouts are `sessions/YYYY/MM/DD/rollout-*.jsonl`; a separate `history.jsonl` contains prompt history.

## Rollout records

A rollout begins with session metadata and then event records. The parser reads `payload.role`, `payload.content`, and the older `payload.message` fallback.

```json
{"timestamp":"2026-07-17T09:00:00Z","type":"session_meta","payload":{"session_id":"session-7","cwd":"/work/api"}}
{"timestamp":"2026-07-17T09:00:01Z","type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"The migration is complete."}]}}
```

`payload.role` is retained. When only `payload.message` is present, deja treats it as a user message. Content may be a string or an array of text-bearing parts. `session_meta` supplies the stable ID and project working directory. Timestamps accept RFC 3339, Unix seconds, or Unix milliseconds.

## Prompt history

Each history line is independent:

```json
{"session_id":"session-7","ts":1784278801,"text":"check the migration"}
```

History entries map to one-message sessions with role `user` and project `history`. The same prompt may also occur in its rollout; consumers should expect this duplication.

## Known quirks and drift

- Rollout files are append-only JSONL and may have a torn final line.
- Events without a payload and non-message payloads are ignored.
- `history.jsonl` duplicates user prompts but lacks assistant responses and project metadata.
- Older records use `payload.message`; current records generally use structured `payload.content`.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
