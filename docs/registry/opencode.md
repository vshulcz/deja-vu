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

opencode 2.0 renamed these tables. A 2.x store keeps sessions in `session_v2`, and folds messages and their parts into one `session_message` table whose `type` column carries the role:

```sql
session_v2(id, project_id, parent_id, directory, title, time_created, time_updated)
session_message(id, session_id, type, seq, time_created, time_updated, data)
```

`session_message.data` is JSON. A user row keeps its text at the top level; an assistant row holds its parts under `$.content`:

```json
{"metadata":{},"time":{"created":1789841584567},"text":"why does TestRetry flake"}
{"time":{"created":1789841585000},"content":[{"type":"text","text":"the timeout is too short"},{"type":"tool","id":"call_1","name":"bash","state":{"status":"completed","input":{"command":"go test ./pkg/"},"content":[{"type":"text","text":"--- FAIL"}],"metadata":{"exit":1}}}]}
```

Two renames matter for the parts: a tool names itself under `$.name` where 1.x wrote `$.tool`, and what it printed is the block list `$.state.content` where 1.x wrote the string `$.state.output`. Times are epoch milliseconds. The `type` values seen on a 2.0.12 store are `user`, `assistant`, `synthetic`, `system`, `idle`, `shell`, `skill`, `compaction`, `model-switched`, `agent-switched` and `location-switched`; deja reads the first two, and the rest are opencode talking to itself.

deja reads both schemas and picks by asking `sqlite_master` for `session_v2`.

### opencode 1.x

The 1.x parser joins three tables:

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
