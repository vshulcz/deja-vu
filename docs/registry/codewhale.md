# CodeWhale

- **ID**: `codewhale`
- **Store**: `${CODEWHALE_HOME:-~/.codewhale}/sessions/<id>.json` — one file per session
- **Store (pre-rebrand)**: `~/.deepseek/sessions/<id>.json`, which CodeWhale migrates into the current root on first access
- **Read overrides**: `DEJA_CODEWHALE_ROOT` replaces the current session directory (the legacy one is still read while it exists); `CODEWHALE_HOME` moves the whole store and turns the legacy root off
- **Format**: JSON — `{schema_version, metadata, messages}`, pretty-printed
- **Needs**: nothing
- **Resume**: `cd <metadata.workspace> && codewhale --resume <id>`, the absolute workspace from the session file, with no `cd` and a note when it is gone (#4362, #4460) — `--session-id` is its alias and `codewhale exec` takes both (checked against 0.9.13)

CodeWhale is a terminal agent written in Rust. It shipped as `deepseek-tui`
until v0.8.41 and under its own name since; the provider integration did not
change with the name. It is not the DeepSeek Harness deja reads as `deepseek` —
a different program with a different store, which is why both entries exist.

`metadata` carries the id, the title CodeWhale derived itself, `created_at`,
`updated_at`, the `workspace` the session was worked in, and
`parent_session_id` when the session came from a fork. A message is
`{role, content}`, where content is the block list Anthropic's API uses —
`text`, `thinking`, `tool_use`, `tool_result` — so the decoding the Cline and
Claude readers already do applies here: a call becomes a command or a file
record, a `tool_result` becomes tool output, error runs included. Since 0.9.6
new turns use `read`, `write`, `edit` and `bash`, where `edit` takes
`edits[{oldText,newText}]` (also sent as a JSON string, or as one top-level
`oldText`/`newText` pair, both read the same way); the older `read_file`, `write_file` and `edit_file`
names are still read for sessions saved before. Commands also run through
`terminal/run` (a PTY session) and `task_shell_start` (a background task), both
under `command`, and are read as commands (#4538). `apply_patch` takes a
unified diff (`--- a/x` / `+++ b/x`, not codex's `*** Begin Patch`) under
`patch`, retargeted to `path` when that is set, or whole files under `replace`
(deprecated alias `changes`) as `{path, content}`; the files, each hunk's
removed lines and the added lines or contents are read, paths under the
workspace. A call whose `tool_result` has `is_error` is not recorded (#4538). A failed `bash` result has
`is_error` and ends "Command exited with code N"; the command it answers, by
`tool_use_id`, carries that as `→ exit N` (#4537).

`thinking` blocks are dropped. So are the `system` and `developer` roles:
CodeWhale's own documentation names them as where it puts compaction summaries,
branch summaries and sub-agent framing, which is the harness talking to itself
rather than anything either side said.

`$CODEWHALE_HOME` is an isolation boundary in CodeWhale's own resolver — with it
set, the legacy root is not consulted — and deja does not reach outside it
either.

**Last verified:** 2026-10-08

## Known quirks and drift

- **No per-message timestamps.** The file stores `created_at` and `updated_at`
  for the session and nothing per message. The session's own start is the clock,
  one millisecond per record, so two identical turns stay two records rather
  than collapsing into one the way they did for Zed (#3333).
- **Bookkeeping sits beside the transcripts.** `offline_queue.json`,
  `session_boot_owners.json` and the `checkpoints/` slot share the sessions
  directory, and 0.10.0 adds a directory per session (approval receipts,
  runtime state), `.late-usage/` and `.work-graph-import-archive/`, which keeps
  copies of migrated sessions. A transcript sits directly in the root, so
  everything in a subdirectory is bookkeeping; the files beside the
  transcripts are named rather than counted, so drift there still shows up as
  an unread file.
- **Checked against a running install.** On a 0.10.0 TUI against a stub
  endpoint (#4802), the session files had the shape above. Every user message
  also carries a `<turn_meta>` text block — the date, the workspace, the
  permission posture — which is the harness talking and is dropped.
- **Wiring.** `deja install codewhale` writes the server into
  `$CODEWHALE_HOME/mcp.json` under `servers` (not `mcpServers`), and adds
  `mcp_deja_deja` to `[tools] always_load` in `config.toml`. CodeWhale boots
  servers lazily; without that entry the tool was missing from the request.
  The skill goes in `~/.agents/skills`, which CodeWhale lists beside its own
  `skills/` and `~/.claude/skills` and shows each copy it finds.
- **Command.** `~/.codewhale/commands/deja.md` is `/deja` in the slash menu
  (0.10.1). The directory is under `$HOME` even with `CODEWHALE_HOME` set. The
  file has no `description`: CodeWhale makes it the session's goal and keeps
  the turn going until the model closes it.
- **Auto-recall.** `deja install codewhale-auto` adds four `[[hooks.hooks]]`
  entries. Hooks fire only in the TUI; `codewhale exec`, the ACP and app
  servers fire none. Of the events, only two put text in front of the model:
  - `message_submit` may replace the message with `{"text": …}`. deja keeps
    the person's text first and appends the session digest (first message of
    a session only) and the prompt's recall, framed in `<deja-recall>`.
    `session_start`'s stdout is discarded, which is why the digest rides here.
    CodeWhale saves the replaced message and shows it in the transcript; the
    framed part is not indexed.
  - `tool_call_before` may add `additionalContext`, appended to the tool's
    result as `[hook context] …` and capped at 2,000 characters. deja sends no
    `decision`, which CodeWhale reads as allow. The line arrives only on a
    tool that ran: a call that fails to run (a `ToolError`, "Failed to …")
    goes back as `Error: …` without it (`turn_loop.rs`, 0.10.1).
  - `session_end` (on `/quit`, not on a kill) ends the live stamp.
  `tool_call_after`, `turn_end` and `session_start` output never reaches the
  model. So `tool_call_after` only parks text for the next `tool_call_before`
  or `message_submit`: the fix pair for a failed shell command, read from the
  execution receipt on its stdin, and the `tool_call_before` line of a call
  that failed to run. The hooks'
  `sess_…` ids are made fresh for each hook executor and are not the store's
  file ids, so a session's live stamp does not keep MCP recall off its own
  transcript.
- **Compaction.** There is no compaction event. Before compacting, CodeWhale
  saves the whole history as
  `sessions/<id>/artifacts/context-transfer-<checkpoint>.json`, and once the
  summary is written, the summary beside it in `.md`
  (`compact_messages_safe`). The next `message_submit` or `tool_call_before`
  takes the newest pair written since that hook session was first seen, for a
  saved session in the same workspace, and the packet rides it. A `.json`
  without its `.md` is a failed attempt or a prune and is skipped. Checked on
  a 0.10.0 stand.
- **Status line.** None to write to: the footer takes only built-in items
  (`StatusItem` in `crates/tui/src/config.rs`), and observer hooks' stdout is
  discarded.
- **Resume.** `codewhale --help` on 0.9.13 lists `--resume`, `--session-id`
  and `--continue`, and `--continue` refuses in a directory with no saved
  session, which is how the per-workspace scoping shows. `codewhale exec`
  does not persist a session; the TUI writes them.
