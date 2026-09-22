# opencode session format

## Store and files

opencode stores sessions in `~/.local/share/opencode/opencode.db`. On Linux it honors `XDG_DATA_HOME`, producing `$XDG_DATA_HOME/opencode/opencode.db`; deja also accepts `DEJA_OPENCODE_DB`. The store is SQLite and deja reads it through the `sqlite3` command-line tool.

Beside the database, opencode writes one file per session recording what that
session changed: `storage/session_diff/ses_<id>.json`, a list of
`{file, status, additions, deletions, patch}` with `patch` in unified-diff form.
On one machine that is 1,002 files, and of 400 of those sessions only 17 held an
edit record from the database — so for the rest it is the only account of what
the session touched. deja reads it and folds the records into the session the
database gives, by id; `DEJA_OPENCODE_DIFFS` overrides where it looks.

## Schema

opencode 2.0 renamed the tables. The migration in the 2.0 binary runs `ALTER TABLE session RENAME TO session_v2`, and a session's turns move out of `message` and `part` into one `session_message` table — one row per turn, the role in its `type` column, an assistant turn's parts inside its `data` blob:

```sql
session_v2(id, project_id, workspace_id, parent_id, slug, directory, path, title, version, ...)
session_message(id, session_id, type, seq, time_created, time_updated, data)
```

A 2.0 store has no `session`, `message` or `part` table at all. Measured on opencode 2.0.12 (`@opencode/cli`), run in a throwaway HOME: the tables are `session_v2`, `session_message`, `session_inbox`, `session_pending` and the rest of the 2.0 set.

A user row keeps its text at the top level; an assistant row holds its parts under `$.content`:

```json
{"time":{"created":1790102971000},"text":"edit main.go so add returns a+b+1"}
{"time":{"created":1790102977894},"agent":"build","content":[{"type":"reasoning","text":"…"},{"type":"tool","id":"call_565d…","name":"shell","state":{"status":"completed","input":{"command":"go vet ./..."},"content":[{"type":"text","text":"pattern ./...: directory prefix . does not contain main module"}],"metadata":{"exit":1}},"time":{"created":1790102980000}},{"type":"text","text":"add now returns a+b+1"}]}
```

What changed inside a turn, measured on the same store:

- a tool names itself under `$.name`; `bash` is now `shell` and `apply_patch` is now `patch`
- what a tool printed is the block list `$.state.content`, where 1.x wrote the string `$.state.output`
- the file a `read`, `edit` or `write` names is `$.state.input.path`, where 1.x wrote `filePath`; it can be relative to the session directory
- `edit` is the editing tool, and it carries both sides: `oldString` and `newString`
- times are epoch milliseconds

The message types on a 2.0 store are `user`, `assistant`, `synthetic`, `system`, `idle`, `shell`, `skill`, `compaction`, `model-switched`, `agent-switched` and `location-switched`. deja reads the first two and a compaction's summary; the rest are opencode talking to itself.

deja reads both layouts and picks by asking where the turns are: `session_message` when it holds rows, `message` and `part` otherwise. The sessions come from `session_v2`, or from `session` on a store that has no `session_v2`.

The per-session diff store (`storage/session_diff/`) is a 1.x store; a 2.0 home does not write it.

### The 1.x layout

### The older layout

### opencode 1.x

The parser joins three tables:

```sql
session(id, directory, time_created, time_updated)
message(id, session_id, time_created, data)
part(id, message_id, data)
```

`message.data` and `part.data` are JSON. A real-shaped pair is:

```json
{"role":"assistant","time":{"created":"2026-07-17T09:00:01Z"}}
{"type":"text","text":"The query now uses the index.","time":{"start":"2026-07-17T09:00:01Z"}}
```

Only parts with `type: "text"` are messages. The role comes from `message.data.role`. Message time prefers `part.data.time.start`, then `message.data.time.created`; session times come from the `session` row. Strings in RFC 3339 form and numeric Unix seconds or milliseconds are accepted. `session.directory` supplies the project.

## Known quirks and drift

- The database can be several gigabytes. deja projects JSON scalars in SQL instead of streaming complete blobs.
- Message content is split across `message` and `part`; one message can have several parts.
- Non-text parts, including tool data, are ignored. Text is capped at 64 KiB per part.
- A missing database must not be passed to SQLite because the CLI would create it.
- The committed conformance fixture is SQL rather than a binary database; the test creates a temporary SQLite file.

**Last verified:** 2026-09-22
