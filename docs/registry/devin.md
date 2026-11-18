# Devin CLI

- **ID**: `devin`
- **Store**: `<data>/devin/cli/sessions.db` — `~/.local/share/devin/cli/sessions.db` on Linux and macOS (verified on both; `XDG_DATA_HOME` honoured); `%APPDATA%\devin\cli\sessions.db` on Windows (verified on 3000.14.30821; `%LOCALAPPDATA%\devin\cli` holds only the installed binary and tools) — with `cli_sessions.db` beside it holding the pre-rename copy
- **Summaries**: `<data>/devin/summaries/<session_id>.md`
- **Read override**: `DEJA_DEVIN_DB` replaces the store path
- **Format**: SQLite, two tables per conversation — `sessions` for the row, `message_nodes` for the turns — plus `prompt_history` for the inline shell commands run between turns
- **Needs**: `sqlite3`

Devin CLI (Cognition's local `devin` binary) keeps one store for every session.
A session is a row of `sessions` plus its nodes in `message_nodes` — but the
table in order is not the conversation. The agent rebuilds its context chain
whenever the system prefix changes, so the table keeps every copy, several of
them sharing their `message_id`s, and `sessions.main_chain_id` names the head
of the live one. Reading backwards over `parent_node_id` from that head and
keeping one copy of each `message_id` gives the conversation the model saw;
chains the main one cannot reach are `run_subagent` runs sharing the same
table, indexed as subagent sessions of the parent.

Every timestamp is unix seconds. A node's `chat_message` JSON is
`{"role":"system|user|assistant|tool","content":…,"tool_calls":[{"id","name",
"arguments"}]}`; the node's own `metadata` column marks rebuilt scaffolding
with `is_system_prefix`, and a tool row carries its exit code at
`chat_message.metadata.extensions."chisel/terminal_output".exit.exit_code`.
A `system` row without the flag is context something injected — a hook's
`additionalContext` lands verbatim — and is kept as tool output.
`prompt_history.is_shell = 1` rows are the commands the user ran between turns.

`devin --resume <id>` takes the sessions row's id and finds the session in the
one global store; the recorded `working_directory` is where it ran.

`deja install devin` writes `~/.config/devin/mcp_config.json` (`%APPDATA%\devin\mcp_config.json` on Windows, matching `devin mcp add --scope user`; project scope is `.devin/mcp_config.json`) and the shared
`~/.agents/skills/deja-history/SKILL.md`, which Devin's own docs list among
its skill directories. `deja install devin-auto` additionally writes Claude-
shaped hooks into the `"hooks"` key of `~/.config/devin/config.json` (`%APPDATA%\devin\config.json` on Windows):
SessionStart and PostCompaction run `hook-context`, UserPromptSubmit runs
`hook-prompt`, PostToolUse runs `hook-tool` (matcher `exec|edit|write|
apply_patch|notebook_edit`) and `hook-tool-after` (matcher
`^exec$`), and SessionEnd runs `hook-session-end`. Devin's hook replies must
name the event that fired, which is why the hooks emit `hook_event_name` from
the payload rather than their compiled-in names; on Claude's events nothing
changes.

`devin-plugin/` in this repo ships the same wiring as a Devin plugin —
`.devin-plugin/plugin.json` with the MCP server, `hooks.json`, and the skill —
installed with `devin plugins install vshulcz/deja-vu#devin-plugin`. Devin's
cloud sessions are reported to fire plugin hooks on a smaller event set —
SessionStart and SessionEnd among the ones not delivered, though the plugin
docs are a closed beta and the list is not public — so on those the opening
digest and the end-of-session marker may be CLI-only; `deja install
devin-auto` stays the recommended path. Where both are present the plugin's
hooks see the wiring in `~/.config/devin/config.json` and stand down.

**Last verified:** 2026-11-18 (Windows paths on Devin CLI 3000.14.30821; store format and hooks on 3000.11.3)

## Known quirks and drift

- **`additionalContext` under PreToolUse is silently dropped.** Devin accepts
  the reply and never delivers the context, so `hook-tool` rides on
  PostToolUse — late for the action that fired it, in time for everything the
  agent does next on that file. Replies must also echo the firing event's name
  in `hookEventName`: a PostToolUse reply that says `"PreToolUse"` is dropped
  the same way (both verified on 3000.11.3).
- **Hook payloads carry no `cwd`.** The launcher sets `DEVIN_PROJECT_DIR` for
  the hook's own process, which `hookCWD` reads after `CLAUDE_PROJECT_DIR`.
- **A rebuilt chain is duplication, not replay.** Nodes after a rebuild repeat
  earlier messages under the same `message_id`; dedupe by `message_id`, not
  position, or a compacted session reads its user turns twice.
- **`subagent_heads` exists but is empty** on the observed builds. When a
  build populates it, its declared `agent_id` names the sub-session; when it
  stays empty, side chains are found structurally — the NULL-parent roots
  the main chain never reaches, which is also how chains a rewind orphaned
  still surface.
- **`PostToolUse` is also the failure event.** Devin has no PostToolUseFailure:
  a nonzero exit arrives as an ordinary PostToolUse with `success: false`, so
  one `hook-tool-after` wiring covers both paths.
- **Hidden sessions are marked, not deleted**: `sessions.hidden = 1`, and the
  parser leaves them out — indexing one would resurrect a session its owner
  removed from view.
