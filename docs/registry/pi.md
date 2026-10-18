# pi (pi.dev coding agent)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.pi/agent/sessions/<encoded-project>/<timestamp>_<uuid>.jsonl` |
| **Env override** | `DEJA_PI_ROOT` |
| **deja parser** | `internal/sources/pi.go` |
| **Last verified** | 2026-09-06 |

## Discovery

pi stores session transcripts under `~/.pi/agent/sessions/`. Each project directory uses the same `--`-encoded path scheme as Claude Code, e.g. `--home-you-code-deja-vu--` for `/home/you/code/deja-vu`. Within each project directory, session files are named `<ISO-timestamp>_<UUID>.jsonl`. The encoding is lossy (`my-app` and `my/app` give the same name), so the header's `cwd` names the project and the directory is the fallback for a header without one (#4427).

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
| `apply_patch` (OpenClaw) | `input`, a `*** Begin Patch` body | each file the patch names, its removed lines, its added lines |
| `bash` (OpenClaw: `exec`; the pi-coding-agent under Senpi and Kimchi also `powershell`) | `command` | the command, and `→ exit N` from the matching `toolResult` (`details.exitCode` when there is one, else the "Command exited with code N" line that ends a failed result, `exit 0` for a result that is not an error) |

A relative `path` resolves against the header's `cwd`. gjc's `edit` takes one `input` string in its hashline form instead; see the gjc entry. omp and gjc also edit in a replace mode, omp's `{path, old_string, new_string}` (or `edits` of those) and gjc's `{path, edits:[{old_text, new_text}]}`, and a patch mode, `{path, edits:[{op, diff}]}`: those give the same records, a patch's `-` lines per hunk the replaced span and its `+` lines, or a created file's whole `diff`, the written lines (#4524).

### Timestamps

Both ISO-8601 strings (`"timestamp"` in the envelope) and Unix milliseconds (`"timestamp"` inside `message`) are observed. The parser uses the envelope timestamp.

## Session identity

The `id` field from the session header line is used as the session ID. The UUID also appears in the filename.

## Package

`pi install npm:@vshulcz/pi-deja` installs the recall extension as a pi package (`pi-package` keyword, `pi.extensions`); it wires the same handlers the installer writes — session start, prompt, `tool_result`, `session_compact` and `session_shutdown` — and stands down when `deja install pi-auto` already wrote `~/.pi/agent/extensions/deja.ts`, or when the omp counterpart is in place.

## MCP

pi 0.99 and later read `~/.pi/agent/mcp.json` themselves, through the built-in `mcp` extension (`pi mcp list` shows the entry). Older pi reads it only through the `pi-mcp-adapter` package (`pi install npm:pi-mcp-adapter`), which also works on a current pi. `deja install pi` writes the `mcpServers` entry there and says so when nothing in the pi on PATH will read it: a pi older than 0.99 without the adapter, or the built-in extension switched off by `-builtin:mcp` (or a `!builtin:*` glob) in `extensions` of the user `settings.json` or, for a project pi trusts, the project's; `deja doctor` then reports the row as `no adapter` (#4583). `PI_CODING_AGENT_DIR` moves the whole agent directory, and deja follows it for the extension, `mcp.json` and the sessions (`PI_CODING_AGENT_SESSION_DIR` moves the sessions alone). Both are read from the environment of the deja process, so set them where deja runs too.

## Skill, auto-recall, command

The skill is the shared `~/.agents/skills/deja-history/SKILL.md`; pi scans that directory, and a second copy in `~/.pi/agent/skills` makes it report a collision, so install removes one an older deja left there.

`deja install pi-auto` writes the MCP entry and `~/.pi/agent/extensions/deja.ts`. The extension returns the session digest on the first turn and per-prompt recall after that at `before_agent_start`, adds to a `tool_result` a file's history after a `read` or the earlier fix after a failed `bash` or `powershell` command, runs `deja hook-precompact` at `session_compact` and `deja hook-session-end` at `session_shutdown`, and registers `/deja <query>`, which runs `deja search`.

`deja resume` prints `pi --session <id>`, run in the `cwd` the session header records; the folder name folds `/` into `-`, so `my-app` and `my/app` share it. With that directory gone the `cd` is left out and deja notes that pi, run from another project, offers to fork the session (#4456).

## Known quirks and drift

- Project directory encoding uses `--` prefix and suffix (e.g. `--home-you-code-foo--`) compared to Claude Code's single `-` prefix. The `resolveEncodedPath` function handles both.
- Version field observed: `3`. No version migration behavior is known.
- The `parentId` chain forms a tree, not a flat list; deja ignores the tree structure and processes messages in file order.

**Last verified:** 2026-09-06
