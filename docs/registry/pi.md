# pi (pi.dev coding agent)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.pi/agent/sessions/<encoded-project>/<timestamp>_<uuid>.jsonl` |
| **Env override** | `DEJA_PI_ROOT` |
| **deja parser** | `internal/sources/pi.go` |
| **Last verified** | 2026-09-06 |

## Discovery

pi stores session transcripts under `~/.pi/agent/sessions/`. Each project directory uses the same `--`-encoded path scheme as Claude Code, e.g. `--Users-max-code-deja-vu--` for `/Users/max/code/deja-vu`. Within each project directory, session files are named `<ISO-timestamp>_<UUID>.jsonl`. The encoding is lossy (`my-app` and `my/app` give the same name), so the header's `cwd` names the project and the directory is the fallback for a header without one (#4427).

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
| `toolResult` | tool output (`RoleToolOutput`) |

### Content

`message.content` is an array of typed blocks. deja extracts `text` from blocks where `"type": "text"`. Blocks with `"type": "thinking"` are skipped.

### Tool calls

A `toolCall` block carries `name` and `arguments`. deja reads them for pi and every harness built on it (omp, OpenClaw, gjc, prime, senpi, Kimchi), so `deja files`, `how`, `restore` and `blame` have something to go on (#4113):

| `name` | Arguments | deja records |
| --- | --- | --- |
| `read` | `path` | the file |
| `edit` | `path`, `edits[].oldText` / `newText` (older pi: one `oldText` / `newText` pair) | the file, the replaced span, the written lines |
| `write` | `path`, `content` | the file and the written lines |
| `bash` (OpenClaw: `exec`) | `command` | the command, and `→ exit N` from the matching `toolResult` (`details.exitCode` when there is one, else the "Command exited with code N" line that ends a failed result, `exit 0` for a result that is not an error) |

A relative `path` resolves against the header's `cwd`. gjc's `edit` takes one `input` string in its hashline form instead; see the gjc entry.

### Timestamps

Both ISO-8601 strings (`"timestamp"` in the envelope) and Unix milliseconds (`"timestamp"` inside `message`) are observed. The parser uses the envelope timestamp.

## Session identity

The `id` field from the session header line is used as the session ID. The UUID also appears in the filename.

## Package

`pi install npm:@vshulcz/pi-deja` installs the recall extension as a pi package (`pi-package` keyword, `pi.extensions`); it wires the same four handlers the installer writes — session start, prompt, `tool_result` and `session_compact` — and stands down when `deja install pi-auto` already wrote `~/.pi/agent/extensions/deja.ts`, or when the omp counterpart is in place.

## MCP

pi does not include built-in MCP but supports it via the `pi-mcp-adapter` package (`pi install npm:pi-mcp-adapter`). The adapter reads `~/.pi/agent/mcp.json` with the standard `mcpServers` shape. `deja install pi` writes to that file.

## Skill, auto-recall, command

The skill is the shared `~/.agents/skills/deja-history/SKILL.md`; pi scans that directory, and a second copy in `~/.pi/agent/skills` makes it report a collision, so install removes one an older deja left there.

`deja install pi-auto` writes the MCP entry and `~/.pi/agent/extensions/deja.ts`. The extension returns the session digest on the first turn and per-prompt recall after that at `before_agent_start`, adds to a `tool_result` a file's history after a `read` or the earlier fix after a failed `bash` command, runs `deja hook-precompact` at `session_compact`, and registers `/deja <query>`, which runs `deja search`.

`deja resume` prints `pi --session <id>`, run in the `cwd` the session header records; the folder name folds `/` into `-`, so `my-app` and `my/app` share it. With that directory gone the `cd` is left out and deja notes that pi, run from another project, offers to fork the session (#4456).

## Known quirks and drift

- Project directory encoding uses `--` prefix and suffix (e.g. `--Users-max-code-foo--`) compared to Claude Code's single `-` prefix. The `resolveEncodedPath` function handles both.
- Version field observed: `3`. No version migration behavior is known.
- The `parentId` chain forms a tree, not a flat list; deja ignores the tree structure and processes messages in file order.

**Last verified:** 2026-09-06
