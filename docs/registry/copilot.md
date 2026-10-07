# Copilot CLI

- **ID**: `copilot`
- **Store**: `${COPILOT_HOME:-~/.copilot}/session-state/<sessionId>/events.jsonl`;
  `COPILOT_HOME` also moves the MCP config, the skill and the hooks below
- **Read override**: `DEJA_COPILOT_ROOT` (points at the `session-state` directory)
- **Format**: append-only JSONL, one event per line, each `{type, data, timestamp}`

`session.start` carries `sessionId` and `context.cwd`, which is where the
project name comes from; the directory the file sits in is not the id and can
differ from it. Conversation turns are `user.message` and `assistant.message`,
both with `data.content` as a plain string.

The work is filed outside the message stream. `tool.execution_start` carries
`toolName` and `arguments` — `path` for the file tools, `command` for `bash`,
and `old_str`/`new_str` for an edit, which is the one place the replaced text
survives. `create` hands over the whole file as `file_text`, and that is the
only record a created file's lines were ever in a session — a commit that adds
them deletes nothing for the replaced side to match. `view` is the file read.
With a GPT model there is no `edit` or `create`: every edit is `apply_patch`,
whose `arguments` is the patch string itself, read into files, the removed
lines and the added ones as codex's patches are. `tool.execution_complete` carries `data.result.content`,
indexed as tool output whether or not the call failed, because the error a
command hit is what a later search reaches for. Its `success` flag is true on
failed runs too, so a non-zero exit code is read from the telemetry or the
trailer in the output and added to the command record. `session.shutdown`
lists `codeChanges.filesModified`; deja does not read it, since the tool events
already name every file.

- **MCP**: `deja install copilot` writes `mcpServers.deja` into
  `${COPILOT_HOME:-~/.copilot}/mcp-config.json`.
- **Skill**: `~/.copilot/skills/deja-history/SKILL.md` (under `$COPILOT_HOME`
  when set), loaded on demand;
  Copilot invokes a skill by name, so it is also the `/deja-history` command.
- **Auto-recall**: `deja install copilot-auto` writes
  `~/.copilot/hooks/deja.json`, which Copilot loads as user-level hooks and VS
  Code Copilot Chat reads too. Older installs put the entries in
  `settings.json` or `config.json`; the install takes them out. Measured on
  1.0.92 against a stub model, every one of these reaches the model:
  - `sessionStart` (`deja hook-context --copilot`): the digest, as a message
    of its own in front of the first request, kept for the session.
  - `userPromptSubmitted` (`deja hook-prompt --copilot`): per-prompt recall,
    appended to the user's turn inside `<system_reminder>`.
  - `preToolUse` (`deja hook-tool --copilot`, matcher `bash|powershell|edit|create`):
    the file's prior decision before an edit, sent as a message after the
    tool's result. Copilot honours the matcher, so deja does not run for reads.
  - `postToolUse` and `postToolUseFailure` (`deja hook-tool-after --copilot`,
    matcher `bash|powershell`): the fix line after a failed command. A non-zero
    exit is still a successful tool call, so `postToolUse` carries most of
    them; a tool that errors fires `postToolUseFailure` with the text in `error`.
  - `PreCompact` (`deja hook-precompact`): spelled the way VS Code reads it;
    Copilot runs a PascalCase key too, with snake_case fields. The payload
    names `events.jsonl` as `transcript_path`; deja captures the session from
    it and hands the packet back on the next tool call or prompt.
  - `preMcpToolCall` (`deja hook-mcp-call`) and `sessionEnd`
    (`deja hook-session-end`): the MCP server is told nothing about who is
    calling, so the first marks the session live before each MCP request and
    recall leaves it out, and the second clears the mark. A `preMcpToolCall`
    hook's output becomes the request's `_meta`, so it prints nothing.

  Every answer is the flat `{"additionalContext": …}`; a nested
  `hookSpecificOutput` runs and reaches no one. `COPILOT_HOME` moves all of
  these files.
- **Resume**: `copilot --resume=<sessionId>`.
- **Handoff**: exec.

## Status line

`deja install copilot-auto` sets `statusLine` in `~/.copilot/settings.json`
(`$COPILOT_HOME`), with `refreshInterval: 1` so a first index build moves on
screen; without it Copilot runs the command on events only. Copilot's
`transcript_path` is the session's directory, which deja reads as the session
id. A status line already set there is left alone. Rendered live on 1.0.79.

## Known quirks and drift

- `assistant.message` records whose content is empty and whose work is entirely
  in tool calls used to be dropped; the tool events now carry them.
- Tool names are lowercase (`edit`, `view`, `create`, `bash`) where Claude Code
  capitalises, and the edit argument is `old_str` rather than `old_string`.
- A session directory can outlive its `events.jsonl`; the discovery walk only
  picks up files that exist.
- Since 1.0.79 user settings live in `settings.json` and `config.json` is
  managed by the CLI. On each start it moves user keys still in `config.json`
  across, and a `hooks` key there replaces the one in `settings.json` whole, so
  while the reader's hooks are still in `config.json` deja writes its entry
  beside them.
- A hook answer in Claude Code's `hookSpecificOutput` envelope is run and
  logged as a success, and its context reaches nobody: Copilot reads only a
  top-level `additionalContext`.

Specified in [#655](https://github.com/vshulcz/deja-vu/issues/655), work
records added in [#1231](https://github.com/vshulcz/deja-vu/pull/1231),
auto-recall in [#4231](https://github.com/vshulcz/deja-vu/issues/4231).

**Last verified:** 2026-10-07
