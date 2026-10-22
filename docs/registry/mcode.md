# MiniMax Code

- **ID**: `mcode`
- **Store**: `~/.minimax/v2/sessions/YYYY/MM/DD/<session>/messages.jsonl`
- **Read overrides**: `DEJA_MCODE_ROOT`, a path, replaces the session root
- **Format**: JSONL, one message per line, `timestamp` in milliseconds
- **Needs**: `sqlite3` for the session's workspace (project, resume directory, compaction capture); the transcripts read without it

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
skill catalogue, the media reminder. deja reads the typed part the way the
runtime itself reads it back:

1. `genuineUserQueryText`, when the turn has it, is the typed text. The runtime
   writes it, beside an empty `canonicalTextRange`, whenever the typed text is
   not the tail of the assembled one.
2. Otherwise `canonicalTextRange` (UTF-16 offsets) is cut out of every text
   block joined with a newline.
3. Otherwise the turn is the assembled text without its `<system-reminder>`
   and `<background-task-finished>` blocks, and a turn that opens
   `[System CronTask …]` or `[Cron Report …]` is a scheduled task, not a person.

A `user` line that carries `archonCompaction` is a compaction marker, not a
turn. Its `summary` is indexed under the summary role, except for the marker a
migration leaves when the older history could not be recovered
(`archonLegacyContextBoundary`), which says only that.

The `thinking` block is the model reasoning out loud, not what it told the
person, so it stays out of the transcript the way it does in every other
harness here.

The transcript records no working directory. The runtime keeps it in
`~/.minimax/v2/sqlite/runtime-state.sqlite` (`local_runtime_sessions`):
`workspace_dir` is where the session ran, `project_workspace_dir` the
repository a worktree session belongs to, and `is_default_workspace` marks a
chat in the runtime's own scratch directory, which names no project. deja reads
them through `sqlite3`; the project is `project_workspace_dir`, else
`workspace_dir`. Without `sqlite3`, or for a default-workspace chat, the
project comes from where the session's own file activity was, the evidence the
other harnesses without a recorded cwd are filed under, and a PreCompact
capture can only match the hook's directory that way.

The session id is the one `manifest.json` records; the directory name carries
it base64url-encoded, and is read when the manifest is missing.

**Last verified:** 2026-10-22

## Sources

The layout was read off MiniMax Code's own local runtime v2 sessions
(`~/.minimax/v2/sessions`); the runtime ships no published format for its
transcripts. The user-text rules, the compaction marker, the base64url
directory name and `--session` were checked against the shipped bundle,
`@minimax-ai/code` 0.6.12 (`J4n`, `bK`, `KJ`, `JDe` in its chunks). The fixture
is synthetic.

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
its input, the session id, the cwd — so the hooks read it unchanged. Its
`transcript_path` is a temporary Claude-compatible projection the runtime
deletes after the call, so the PreCompact capture reads the session from the
store by its id instead. What a hook answers comes back in the turn and the
tool result as `<plugin-hook-context>`, and is left out of the index. Only the
way the answer is written back is MiniMax Code's own: `--mcode` puts the text
under `hookSpecificOutput.additionalContext`, under the event it answers,
which is the only place that runtime reads it from.

`deja handoff --to mcode --exec` runs the MiniMax Code CLI with the digest as
its first prompt. The desktop app is not a command line, so the handoff needs
the CLI (`mcode`) on the machine.

`deja resume <id>` prints `mcode --session <id>`, which opens the session by
the id its manifest records from any directory, run in the session's
workspace when the runtime's database names one that still exists.
