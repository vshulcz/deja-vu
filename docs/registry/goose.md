# Goose

- **ID**: `goose`
- **Store (legacy)**: `~/.local/share/goose/sessions/*.jsonl` (pre-1.10.0; files remain on disk after migration)
- **Store (current)**: `~/.local/share/goose/sessions/sessions.db` (SQLite, Goose >= 1.10.0)
- **Linux relocation**: `$XDG_DATA_HOME/goose/sessions/...`
- **Read override**: `DEJA_GOOSE_ROOT` (takes precedence for reads); `DEJA_GOOSE_DB` for the SQLite path
- **Format**: legacy JSONL (metadata header + message records) and SQLite relational store

Legacy JSONL: the first line is session metadata (`description`, `id`, `working_dir`,
`created_at`, `updated_at`); subsequent lines are messages with `role`, `created` (unix
seconds) and `content` blocks (`type: text` only for v1). SQLite: `sessions` joined to
`messages` on `session_id`; `content_json` is a JSON array of content blocks.

- **MCP**: `deja install goose` adds the server as an extension in
  `~/.config/goose/config.yaml`.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`.
- **Command**: Goose declares commands in the same config rather than a
  commands directory, so `/deja` is a recipe entry there.
- **Auto-recall**: `SessionStart` and `UserPromptSubmit` hooks in
  `~/.agents/plugins/deja/hooks/hooks.json`. Goose discards what a hook prints,
  so neither answers on stdout: they write the file Goose re-reads, which is
  `.goosehints` at session start and the MOIM file per prompt.
- **Resume**: `goose session --resume --session-id <id>`.
- **Handoff**: exec, `goose run -t`.
- **Prerequisite**: the per-prompt half needs `GOOSE_MOIM_MESSAGE_FILE`, which
  the `deja goose` wrapper sets.

## What the other nine hook events can carry

Goose has eleven hook events (twelve on 1.49, which adds `PreToolUseResult`).
deja wires two. The rest were measured on a stand — isolated HOME, a recording
endpoint, a hook on every event writing a marker into the MOIM file and a
`{"additionalContext": …}` on stdout — on 1.46.0 and again on 1.49.0, with the
same result both times.

One turn whose shell command exits 3 fires `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `BeforeShellExecution`, `PostToolUseFailure`, `Stop` and
`SessionEnd`; `PostToolUse` and `AfterShellExecution` do not fire on a failure.
Every tool event carries the same four fields — `event`, `session_id`,
`tool_name`, `tool_input`, plus `working_dir` — and no output, no error text and
no exit code. `Stop` carries `last_assistant_message`.

Two things follow, and they are why the other nine stay unwired:

- Hook stdout is discarded. No marker written there appeared in any recorded
  request, on either version.
- The MOIM file is re-read at each prompt, not between tool calls. A marker
  written by `PostToolUseFailure` is absent from the request Goose sends
  straight after the failure and present in the next prompt's. So a fix pair
  wired to that event would arrive one turn late, where the prompt hook already
  speaks — and with no error in the payload to key it on (#2952, #2956).

Requested in [#255](https://github.com/vshulcz/deja-vu/issues/255).

**Last verified:** 2026-09-07
