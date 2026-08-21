# omp (Oh My Pi)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.omp/agent/sessions/<encoded-project>/<ISO-timestamp>_<uuid>.jsonl` |
| **Env override** | `DEJA_OMP_ROOT` |
| **deja parser** | `internal/sources/omp.go` |
| **Last verified** | 2026-08-21 |

## Discovery

omp (Oh My Pi, `github.com/can1357/oh-my-pi`) stores session transcripts under
`~/.omp/agent/sessions/`. Each project directory uses Claude Code's single-dash
path encoding, e.g. `-Code-pleasure-course` for `/Users/halo/Code/pleasure-course`.
Within each project directory, session files are named
`<ISO-timestamp>_<uuid>.jsonl`.

That is the default profile's store, and it is not the only one. A named
profile (`omp --profile <name>`, `OMP_PROFILE`, or the legacy `PI_PROFILE`)
relocates omp's whole user scope, sessions included, and an `omp` directory
under `XDG_DATA_HOME` relocates it again — with no `agent` segment there. deja
reads all four:

| Where | Sessions |
| --- | --- |
| Default profile | `~/.omp/agent/sessions/` |
| Named profile | `~/.omp/profiles/<name>/agent/sessions/` |
| XDG | `$XDG_DATA_HOME/omp/sessions/` |
| XDG, named profile | `$XDG_DATA_HOME/omp/profiles/<name>/sessions/` |

`DEJA_OMP_ROOT` overrides all of them: with it set, that directory is the only
one read. XDG counts only when `$XDG_DATA_HOME/omp` exists, since the variable
itself is set on most Linux desktops whether or not omp lives there.

Verified by running omp 17.4.1 each way and following where the transcript
landed.

## File layout

Each `.jsonl` file is a single session. The first line is a session header:

```json
{"type":"session","version":3,"id":"<uuid>","timestamp":"<ISO-8601>","cwd":"<absolute-path>","title":"..."}
```

Subsequent lines are typed events:

| `type` | Description |
| --- | --- |
| `session` | Session header (first line only) |
| `title` / `title_change` | Title metadata |
| `model_change` | Model/provider switch |
| `thinking_level_change` | Thinking level adjustment |
| `message` | User prompt, assistant response, or tool result |
| `custom` | Tool execution lifecycle events (skipped) |

## Message records

Messages use a wrapper envelope:

```json
{
  "type": "message",
  "id": "<hex>",
  "parentId": "<hex-or-null>",
  "timestamp": "<ISO-8601>",
  "message": {
    "role": "user|assistant|toolResult|developer",
    "content": [{"type": "text", "text": "..."}],
    "timestamp": 1786964225859
  }
}
```

### Roles

| `message.role` | deja maps to |
| --- | --- |
| `user` | `user` |
| `assistant` | `assistant` |
| `toolResult` | tool output (`RoleToolOutput`) |
| `developer` | skipped |

### Content

`message.content` is an array of typed blocks. deja extracts `text` from blocks
where `"type": "text"`. Blocks with `"type": "thinking"`, `"type": "toolCall"`,
or `"type": "image"` are skipped.

### Timestamps

Both ISO-8601 strings (`"timestamp"` in the envelope) and Unix milliseconds
(`"timestamp"` inside `message`) are observed. The parser uses the envelope
timestamp.

## Session identity

The `id` field from the session header line is used as the session ID. The UUID
also appears in the filename.

## Project attribution

omp's session header carries a real `cwd`, which is promoted to the project key
(`useHeaderCwd=true` in the shared pi-lineage parser). This is more accurate
than decoding the single-dash directory name, which drops the leading path
segment (`-Code-pleasure-course` would otherwise decode to `pleasure/course`).

## MCP, skill, command

`deja install omp` writes `~/.omp/agent/mcp.json` with the standard `mcpServers`
shape — the file upstream documents as the primary user-scope MCP config. After
it, omp lists deja's tools itself (`mcp__deja_recall`, `recall_context`,
`remember`, `blame`, `fix`, `how`).

The skill is the shared `~/.agents/skills/deja-history/SKILL.md`: omp's `agents`
provider reads that directory, so it needs no file of its own. Checked by asking
a running omp to list its skills.

The command is `~/.omp/agent/commands/deja.md`. Typing `/deja <question>` runs
recall and answers from the result — verified end to end against a live omp
answering from an earlier omp session.

A named profile reads its own `mcp.json` and `commands/` under
`~/.omp/profiles/<name>/agent/`; install writes the default profile's, which is
what a person on the default profile reads.

Auto-recall is still open: omp has an extension/hook API (`--hook`,
`--extension`) and deja writes no extension for it yet.

## Known quirks and drift

- Project directory encoding uses Claude Code's single `-` prefix (e.g.
  `-Code-foo`) rather than pi's `--` prefix/suffix. The shared
  `resolveEncodedPath` handles both, but omp relies on the header `cwd` instead.
- `toolResult` messages carry a `toolCallId`/`toolName` and a `content` array of
  `text` blocks; deja indexes the text as tool output.
- The `parentId` chain forms a tree, not a flat list; deja ignores the tree
  structure and processes messages in file order.

**Last verified:** 2026-08-21
