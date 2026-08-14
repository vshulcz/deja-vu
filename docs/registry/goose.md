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

- **MCP**: Goose uses `~/.config/goose/config.yaml` extensions — no `deja install` target yet.
- **Resume**: `goose session --resume --session-id <id>` (verified against upstream docs).
- **Handoff**: paste — no documented start-with-prompt flag.

Requested in [#255](https://github.com/vshulcz/deja-vu/issues/255).

**Last verified:** 2026-07-28
