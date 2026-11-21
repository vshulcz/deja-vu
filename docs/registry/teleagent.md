# TeleAgent

- **ID**: `teleagent`
- **Store**: `${TELEAGENT_HOME:-${XDG_DATA_HOME:-~/.local/share}/TeleAgent}/users/<account>/teleagent.db` — one SQLite store per account (`DEJA_TELEAGENT_ROOT` replaces the whole tree)
- **Format**: SQLite (OpenCode message schema)
- **Needs**: `sqlite3`

TeleAgent (星辰超级智能体, the China Telecom TeleAI desktop client) keeps one
OpenCode-schema SQLite store per account under `users/<account>/teleagent.db`.
Its tables — `session`, `message`, and `part`, or `session_v2` and
`session_message` — use OpenCode's message schema unchanged, so deja reads
sessions through the same SQLite reader (`parseOpencodeSchemaDB` with harness
`"teleagent"`). See [OpenCode](opencode.md) for the field-by-field schema
description. The on-disk format was surveyed by the reference converter
([Nwflower/dsh-chat-import](https://github.com/Nwflower/dsh-chat-import),
issue #60, commit 7b5453c); deja's read of it is validated against synthetic
fixtures built to those surveyed shapes.

- **No install surface**: the desktop client exposes no config directory, MCP
  client, skill catalogue, user-definable slash commands, or CLI prompt, so
  there is nothing for `deja install` to wire and no `--exec` handoff target;
  its history is still indexed and searchable from every other agent deja
  serves, and `deja handoff --to teleagent` prints the digest to paste.

**Last verified:** 2026-09-17 (reference-converter survey; deja reads it
through fixtures built to the surveyed shapes)
