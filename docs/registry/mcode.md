# MiniMax Code

- **ID**: `mcode`
- **Store**: `~/.minimax/v2/sessions/YYYY/MM/DD/<session>/messages.jsonl`
- **Read overrides**: `DEJA_MCODE_ROOT`, a path, replaces the session root
- **Format**: JSONL, one message per line, `timestamp` in milliseconds
- **Needs**: nothing

MiniMax Code is the desktop agent. Its local runtime keeps one directory per
conversation, sharded by the day it started, and writes the transcript beside a
`manifest.json`:

```
~/.minimax/v2/sessions/2026/10/20/02-57-27-477-session_<id>/
    manifest.json
    messages.jsonl
```

Every line is one message:

```json
{"message_id":"msg-user-v1-…","turn_id":"…",
 "message":{"role":"user","content":[{"type":"text","text":"…"}],
            "timestamp":1792465048498,
            "canonicalTextRange":{"startOffset":110,"endOffset":145}}}
```

The role is `user`, `assistant` or `toolResult`. A content block is `text`,
`thinking` or `toolCall` — `{type, id, name, arguments}` — and a tool result
carries the `toolCallId` and `toolName` it answers.

A fourth role, `compactionSummary`, is what a compaction leaves behind. The
runtime rewrites the transcript in place: the summary of the turns it cut takes
their place, and the turns themselves move to the runtime's own `snapshots/`
directory. The summary is therefore the only record of them the transcript
holds, and deja indexes it under the summary role — dropped it would be the
half of the conversation nobody could find, and read as speech it would be
words the person never said.

A person's turn does not hold only what the person wrote. The runtime
assembles the turn around it: the agent banner, the permission notice, the
skill catalogue, the media reminder. `canonicalTextRange` says which part of
that text was typed, and deja keeps that part alone — the rest is the
harness talking to itself, and indexing it would put the runtime's own words
at the head of every session on the machine.

The `thinking` block is the model reasoning out loud, not what it told the
person, so it stays out of the transcript the way it does in every other
harness here.

The transcript records no working directory, so the project comes from where
the session's own file activity was — the same evidence the other harnesses
without a recorded cwd are filed under.

The session id is the one `manifest.json` records; the directory name carries
it base64-encoded, and is read when the manifest is missing.

**Last verified:** 2026-10-20

## Sources

The shape was read off MiniMax Code's own local runtime v2 sessions
(`~/.minimax/v2/sessions`), not off documentation: the runtime ships no
published format for its transcripts. The fixture is synthetic.

## Wiring

`deja install mcode` writes the server into `~/.minimax/mcp.json`, the file
MCode's own MCP page writes:

```json
{"mcpServers": {"deja": {"type": "stdio", "command": "deja", "args": ["mcp"]}}}
```

`deja install mcode-auto` adds a MiniMax-format plugin package under
`~/.minimax/plugins/deja/`, whose hooks are the events the runtime fires:

| Event | Hook | Matcher |
| --- | --- | --- |
| `SessionStart` | `deja hook-context --mcode` | — |
| `UserPromptSubmit` | `deja hook-prompt --mcode` | — |
| `PreToolUse` | `deja hook-tool --mcode` | `bash\|edit\|write\|read` |
| `PostToolUse` | `deja hook-tool-after --mcode` | `bash` |
| `SessionEnd` | `deja hook-session-end` | — |
| `PreCompact` | `deja hook-precompact --harness mcode` | — |

The payload is Claude-shaped — the event name, the prompt, the tool name and
its input, the session id, the cwd — so the hooks read it unchanged. Only the
way the answer is written back is MiniMax Code's own: `--mcode` puts the text
under `hookSpecificOutput.additionalContext`, under the event it answers,
which is the only place that runtime reads it from.

`deja handoff --to mcode --exec` runs the MiniMax Code CLI with the digest as
its first prompt. The desktop app is not a command line, so the handoff needs
the CLI (`mcode`) on the machine.
