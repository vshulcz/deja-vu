# opencode session format

## Store and files

opencode stores sessions in `~/.local/share/opencode/opencode.db`. On Linux it honors `XDG_DATA_HOME`, producing `$XDG_DATA_HOME/opencode/opencode.db`; deja also accepts `DEJA_OPENCODE_DB`. The store is SQLite and deja reads it through the `sqlite3` command-line tool.

## Schema

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

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
