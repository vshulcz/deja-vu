# Muse Code

- **ID**: `muse`
- **Store**: `${XDG_DATA_HOME:-~/.local/share}/muse/sessions/YYYY/MM/DD/<id>/session.jsonl` — the same home-relative path on macOS, Linux and Windows
- **Subagents**: `<id>/subagent/<child>/session.jsonl`, read only with `DEJA_INCLUDE_SUBAGENTS=1`
- **Read overrides**: `DEJA_MUSE_ROOTS`, a path list, replaces the session roots
- **Format**: event-sourced JSONL, one record per line, `recorded_at` in microseconds
- **Needs**: nothing

Muse Code is Meta's terminal coding agent. It keeps a directory per session
under the day it started; besides `session.jsonl` the directory holds
`cron.db`, `goals.db`, a `tool-outputs/` spill directory and, while `muse`
runs, its locks and sockets. None of those is a transcript, and `deja doctor`
does not count them as files it failed to read.

Each line is `{schema_version, id, stream, sequence, recorded_at, record_type,
payload_type, payload}`, or a `retained_frame` whose `children[].record_json`
are whole records as JSON strings. deja unwraps the frame and reads the
children like any other line.

```json
{"stream":{"kind":"session","id":"<id>"},"recorded_at":1789790455896395,
 "payload_type":"runtime.session",
 "payload":{"kind":"run","event":{"kind":"started","prompt":"..."}}}
```

The conversation is the `run` events of `payload_type: "runtime.session"`:

- `started` with a `prompt` is the person's turn. Task starts share the kind
  and carry no prompt, and a scheduled run's prompt is empty; neither is a turn.
- `assistant_message_committed` carries the reply under `text`.
- `assistant_tool_calls_committed` carries `tool_calls[{call_id, name, args}]`,
  `args` a JSON string; `tool_result_batch_committed` carries
  `results[{tool_call_id, text}]`. `bash {command}` is read as a command;
  its result is a JSON object `{command, exit_code, output, ...}`, so the
  output is indexed and a non-zero `exit_code` goes on the command as
  `→ exit N`. `read_file`, `write_file` and `edit_file {path, find, replace}`
  are read as files, written lines and edit spans.

The session id is `stream.id` of the session's own records. The parent log
also holds each subagent's task-stream records under the task's stream id;
those are skipped, or every subagent objective would read as something the
person typed. The workspace is `workspace_root` in the
`runtime.session.metadata` record, else `cwd` in
`runtime.session.route_facts`, else `workspace_root` in
`session.workspace_branch.observed`; the title is the latest
`session.name.changed` `new_name`.

**Last verified:** 2026-10-08

## Sources

The shapes are not from Meta's documentation. They come from readers built
against real sessions: AstroQore/agent-session-kit's `MuseSessionAdapter`
(PR #21, muse 1.3.0), specstory's `musecode` provider, and tokscale's
`sessions/muse.rs` (PR #1349), which also notes that the parent's
`workflow_child_lifecycle` usage duplicates the subagent logs. The fixture is
synthetic.

## Wiring

Measured on Muse Code 1.4.2 against a local provider stub, without a login
(#4709). `deja install muse` writes the server into
`${XDG_CONFIG_HOME:-~/.config}/muse/settings.json`:

```json
{"mcpServers": {"deja": {"type": "stdio", "command": "deja", "args": ["mcp"], "mode": "optional"}}}
```

The model sees the tool `deja` under Muse's `mcp__<server>` namespace. Muse still loads the legacy
`mcp_servers` key on its own, but with both keys present it loads no MCP
server at all, so install writes into whichever key the file already has and
refuses a file that has both.

`deja install muse-auto` adds hooks to the same file in Claude Code's shape,
`{"hooks": {Event: [{matcher, hooks: [{type, command, timeout}]}]}}`, timeout
in seconds: SessionStart, UserPromptSubmit, PreToolUse on
`bash|edit_file|write_file`, PostToolUse and PostToolUseFailure on `bash`,
PreCompact and SessionEnd. They fire in an untrusted workspace too, the payload is Claude
Code's with `transcript_path: null`, and what a hook returns, as
`hookSpecificOutput.additionalContext` or plain stdout, reaches the model as a
developer message. `edit_file` and `write_file` name the file under
`tool_input.path`.

Muse reads user skills from `~/.agents/skills`, so install writes the
deja-history skill there. Muse lists a user skill in its slash menu, so the
skill is the command: `/deja-history <query>` loads it with the query (1.4.3).

`deja resume <id>` prints `muse resume <id>` in the session's
`workspace_root`: Muse finds the session from anywhere but takes the directory
it runs in as the workspace (#4710). A subagent is not resumable on its own
(`has no saved log`), so it is refused with the parent's id.

Under `DEJA_INCLUDE_SUBAGENTS=1` only children that name a workspace are read.
Muse also logs its reminder and verification observers as children; their
prompt is Muse's own instruction text and they record no workspace (#4711). A
workflow child names its workspace in `session.workspace_branch.observed`
(#4712).
