# pi (pi.dev coding agent)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.pi/agent/sessions/<encoded-project>/<timestamp>_<uuid>.jsonl` |
| **Env override** | `DEJA_PI_ROOT` |
| **deja parser** | `internal/sources/pi.go` |
| **Last verified** | 2026-07-19 |

## Discovery

pi stores session transcripts under `~/.pi/agent/sessions/`. Each project directory uses the same `--`-encoded path scheme as Claude Code, e.g. `--Users-max-code-deja-vu--` for `/Users/max/code/deja-vu`. Within each project directory, session files are named `<ISO-timestamp>_<UUID>.jsonl`.

## File layout

Each `.jsonl` file is a single session. The first line is always a session header:

```json
{"type":"session","version":3,"id":"<uuid>","timestamp":"<ISO-8601>","cwd":"<absolute-path>"}
```

Subsequent lines are typed events:

| `type` | Description |
| --- | --- |
| `session` | Session header (first line only) |
| `model_change` | Model/provider switch |
| `thinking_level_change` | Thinking level adjustment |
| `message` | User prompt, assistant response, or tool result |

## Message records

Messages use a wrapper envelope:

```json
{
  "type": "message",
  "id": "<hex>",
  "parentId": "<hex-or-null>",
  "timestamp": "<ISO-8601>",
  "message": {
    "role": "user|assistant|toolResult",
    "content": [{"type": "text", "text": "..."}],
    "timestamp": 1784448616190
  }
}
```

### Roles

| `message.role` | deja maps to |
| --- | --- |
| `user` | `user` |
| `assistant` | `assistant` |
| `toolResult` | skipped (tool output, not conversational) |

### Content

`message.content` is an array of typed blocks. deja extracts `text` from blocks where `"type": "text"`. Blocks with `"type": "thinking"` or `"type": "toolCall"` are skipped.

### Timestamps

Both ISO-8601 strings (`"timestamp"` in the envelope) and Unix milliseconds (`"timestamp"` inside `message`) are observed. The parser uses the envelope timestamp.

## Session identity

The `id` field from the session header line is used as the session ID. The UUID also appears in the filename.

## MCP

pi does not include built-in MCP but supports it via the `pi-mcp-adapter` package (`pi install npm:pi-mcp-adapter`). The adapter reads `~/.pi/agent/mcp.json` with the standard `mcpServers` shape. `deja install pi` writes to that file.

## Known quirks and drift

- Project directory encoding uses `--` prefix and suffix (e.g. `--Users-max-code-foo--`) compared to Claude Code's single `-` prefix. The `resolveEncodedPath` function handles both.
- Version field observed: `3`. No version migration behavior is known.
- The `parentId` chain forms a tree, not a flat list; deja ignores the tree structure and processes messages in file order.
