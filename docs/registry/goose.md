# Goose

- **ID**: `goose`
- **Store (current, macOS and Linux)**: `$XDG_DATA_HOME/goose/sessions/sessions.db`, default `~/.local/share/goose/sessions/sessions.db` (SQLite, Goose >= 1.10.0). goose resolves its directories through etcetera's `choose_app_strategy`, which is XDG on macOS too; the Apple layout is `choose_native_strategy` (`crates/goose/src/config/paths.rs`)
- **Store (current, Windows)**: `%APPDATA%\Block\goose\data\sessions\sessions.db`
- **Store (legacy)**: `sessions/*.jsonl` in any of those roots (pre-1.10.0; files remain on disk after migration), plus `~/.local/share/Block/goose`, `~/Library/Application Support/Block/goose` and `~/Library/Application Support/goose`
- **Relocation**: `GOOSE_PATH_ROOT` moves config, data and state together, and is then the whole answer
- **Read override**: `DEJA_GOOSE_ROOT` (takes precedence for reads); `DEJA_GOOSE_DB` for the SQLite path
- **Format**: legacy JSONL (metadata header + message records) and SQLite relational store

Legacy JSONL: the first line is session metadata (`description`, `id`, `working_dir`,
`created_at`, `updated_at`); subsequent lines are messages with `role`, `created` (unix
seconds) and `content` blocks (`type: text` only for v1). SQLite: `sessions` joined to
`messages` on `session_id`; `content_json` is a JSON array of content blocks.

- **MCP**: `deja install goose` adds the server as an extension in
  `~/.config/goose/config.yaml` (`$XDG_CONFIG_HOME/goose`,
  `%APPDATA%\Block\goose\config` on Windows, `$GOOSE_PATH_ROOT/config` when
  that is set).
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`.
- **Command**: Goose declares commands in the same config rather than a
  commands directory, so `/deja` is a recipe entry there.
- **Auto-recall**: `SessionStart` and `UserPromptSubmit` hooks in
  `~/.agents/plugins/deja/hooks/hooks.json` (`$GOOSE_PATH_ROOT/.agents/plugins`
  when that is set). Goose discards what a hook prints,
  so neither answers on stdout: they write the file Goose re-reads, which is a
  marked block in `~/.config/goose/AGENTS.md` at session start and the MOIM file
  per prompt. `.goosehints` is where the block used to go; what deja wrote there
  is cleared, and anything else in that file is left alone. A `SessionEnd`
  hook runs `deja hook-session-end`, so the session is back in the next one's
  MCP recall straight away. A `Stop`
  hook runs `deja hook-stop`: a Stop hook's `{"decision":"block","reason"}`
  is the one hook answer goose puts in front of the model, as a user message
  in the same turn (agent.rs). It blocks only when something waits: the fix
  pair for a command that failed this turn, read from `sessions.db` because
  the failure event carries no output, the packet for a compaction this
  turn, found in `sessions.db` by its agent-only note, or the pre-edit line
  for each file the turn's `edit`, `write` or `text_editor` calls changed.
  Each is handed over once.
- **Status line**: goose has none. The one hook output it shows the person is
  a `SessionStart` hook's `{"banner": …}`, printed once when an interactive
  `goose session` opens (goose-cli session/mod.rs), so the `SessionStart`
  hook prints `deja statusline` there. `goose run` shows no banner.
- **Resume**: `goose session --resume --session-id <id>`. A session no longer in `sessions.db` (deleted in goose) is refused with a pointer to `deja show`.
- **Handoff**: exec, `goose run -t`.
- **Prerequisite**: the per-prompt half needs `GOOSE_MOIM_MESSAGE_FILE`, which
  the `deja goose` wrapper sets.

## What the other hook events can carry

Goose has eleven hook events (twelve on 1.49, which adds `PreToolUseResult`).
deja wires four. Apart from `SessionEnd` and `Stop`, the rest were measured on a stand — isolated HOME, a recording
endpoint, a hook on every event writing a marker into the MOIM file and a
`{"additionalContext": …}` on stdout — on 1.46.0 and again on 1.49.0, with the
same result both times.

One turn whose shell command exits 3 fires `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `BeforeShellExecution`, `PostToolUseFailure`, `Stop` and
`SessionEnd`; `PostToolUse` and `AfterShellExecution` do not fire on a failure.
Every tool event carries the same four fields — `event`, `session_id`,
`tool_name`, `tool_input`, plus `working_dir` — and no output, no error text and
no exit code. `Stop` carries `last_assistant_message`.

Two things follow, and they are why the others stay unwired:

- Hook stdout is discarded. No marker written there appeared in any recorded
  request, on either version.
- The MOIM file is re-read at each prompt, not between tool calls. A marker
  written by `PostToolUseFailure` is absent from the request Goose sends
  straight after the failure and present in the next prompt's. So a fix pair
  wired to that event would arrive one turn late, where the prompt hook already
  speaks — and with no error in the payload to key it on (#2952, #2956).

Requested in [#255](https://github.com/vshulcz/deja-vu/issues/255).

**Last verified:** 2026-09-07
