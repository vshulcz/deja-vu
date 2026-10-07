# opencode session format

## Store and files

opencode stores sessions in `~/.local/share/opencode/opencode.db`. `XDG_DATA_HOME` moves it to `$XDG_DATA_HOME/opencode/opencode.db` on every OS, and `OPENCODE_DB` names the file, absolute or relative to that directory. When `XDG_DATA_HOME` is set but no store exists there, deja reads the default one. `DEJA_OPENCODE_DB` overrides all of this. The store is SQLite and deja reads it through the `sqlite3` command-line tool.

Beside the database, opencode writes one file per session recording what that
session changed: `storage/session_diff/ses_<id>.json`, a list of
`{file, status, additions, deletions, patch}` with `patch` in unified-diff form.
On one machine that is 1,002 files, and of 400 of those sessions only 17 held an
edit record from the database — so for the rest it is the only account of what
the session touched. deja reads it and folds the records into the session the
database gives, by id; `DEJA_OPENCODE_DIFFS` overrides where it looks.

## Schema

opencode 2.0 moved the tables. Sessions live in `session_v2`, and a session's turns move out of `message` and `part` into one `session_message` table: one row per turn, the role in its `type` column, an assistant turn's parts inside its `data` blob:

```sql
session_v2(id, project_id, workspace_id, parent_id, slug, directory, path, title, version, ...)
session_message(id, session_id, type, seq, time_created, time_updated, data)
```

A fresh 2.0 store has no `session`, `message` or `part` table at all. Measured on opencode 2.0.12 (`@opencode/cli`), run in a throwaway HOME: the tables are `session_v2`, `session_message`, `session_inbox`, `session_pending` and the rest of the 2.0 set.

A store upgraded from 1.x keeps both sets. The 2.0 migration creates `session_v2` beside `session` and drops none of `session`, `message` or `part`. opencode's v1 migration (`packages/core/src/database/v1-migration.bun.ts`) then copies each old session into `session_v2` under the same id and rewrites its turns into `session_message`, one session per transaction; a session it has not reached yet has its turns only in `message` and `part`. One such store, from opencode 2.0.18, held 5 sessions in `session`, 10 in `session_v2`, 483 rows in `message` and 58 in `session_message` (#4151).

A user row keeps its text at the top level; an assistant row holds its parts under `$.content`:

```json
{"time":{"created":1790102971000},"text":"edit main.go so add returns a+b+1"}
{"time":{"created":1790102977894},"agent":"build","content":[{"type":"reasoning","text":"…"},{"type":"tool","id":"call_565d…","name":"shell","state":{"status":"completed","input":{"command":"go vet ./..."},"content":[{"type":"text","text":"pattern ./...: directory prefix . does not contain main module"}],"metadata":{"exit":1}},"time":{"created":1790102980000}},{"type":"text","text":"add now returns a+b+1"}]}
```

What changed inside a turn, measured on the same store:

- a tool names itself under `$.name`; `bash` is now `shell` and `apply_patch` is now `patch`
- what a tool printed is the block list `$.state.content`, where 1.x wrote the string `$.state.output`
- the file a `read`, `edit` or `write` names is `$.state.input.path`, where 1.x wrote `filePath`; it can be relative to the session directory
- `edit` is the editing tool, and it carries both sides: `oldString` and `newString`; `write` carries the whole file under `content`
- times are epoch milliseconds

The message types on a 2.0 store are `user`, `assistant`, `synthetic`, `system`, `idle`, `shell`, `skill`, `compaction`, `model-switched`, `agent-switched` and `location-switched`. deja reads the first two and a compaction's summary; the rest are opencode talking to itself.

deja reads both layouts and picks by asking where the turns are: `session_message` when it holds rows, `message` and `part` otherwise. The sessions come from `session_v2`, or from `session` on a store that has no `session_v2`. A store that has `session_v2` beside `session`, `message` and `part` is read both ways at once: a session with turns in `session_message` is read from there, since that copy is the one opencode keeps writing to, and every other session in `session` is read from `message` and `part`. Session and message counts, titles, parents and the newest session come from both tables, each session counted once.

The per-session diff store (`storage/session_diff/`) is a 1.x store; a 2.0 home does not write it.

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

Parts with `type: "text"` are messages; the role comes from `message.data.role`. Parts marked `synthetic` or `ignored` are opencode's own text and are dropped, and a message with `summary` set is a compaction digest, indexed under the summary role. Five tool parts are read as well: `read` gives a file record from `state.input.filePath`, `bash` a command record from `state.input.command` with a non-zero `state.metadata.exit` and its `state.output` as tool output, `apply_patch` edit records from `state.input.patchText`, `edit` the replaced `oldString` and the written `newString`, and `write` the written `content`. An `edit` or `write` in the `error` state changed nothing and is not recorded. Message time prefers `part.data.time.start`, then `message.data.time.created`, then the `message.time_created` column; session times come from the `session` row. Strings in RFC 3339 form and numeric Unix seconds or milliseconds are accepted. `session.directory` supplies the project, `session.title` the title, and `session.parent_id` marks a subagent run.

## Wiring

- **MCP**: `deja install opencode` adds a `mcp.deja` entry of type `local` to `opencode.json` (or `opencode.jsonc` when that is the one present) in `$XDG_CONFIG_HOME/opencode`, else `~/.config/opencode`.
- **Skill**: `~/.config/opencode/skills/deja-history/SKILL.md`.
- **Command**: `~/.config/opencode/commands/deja.md`, invoked as `/deja`.
- **Auto-recall**: `deja install opencode-auto` also writes a plugin, `~/.config/opencode/plugins/deja.js`. It puts the session digest into the first system block (`experimental.chat.system.transform`), appends per-prompt recall to the user message it was asked for, in every model call after it too, since opencode rebuilds each call from its store (`experimental.chat.messages.transform`), adds recall to a spawned `task` agent's prompt (`tool.execute.before`), appends a file's history or a failed command's earlier fix to the tool output (`tool.execute.after`), runs `deja hook-precompact` at `experimental.session.compacting`, and on 1.x runs `deja hook-session-end` at `session.idle` and when the plugin is disposed, so a finished session is back in the next one's MCP recall. The plugin shape follows the installed opencode's major version (`opencode --version`, or `DEJA_OPENCODE_MAJOR` where the binary is not on `PATH`), and the store's layout when neither answers: 2.0 loads only a default `{ id, setup }`, 1.x a named export. `DEJA_OPENCODE_MAJOR` picks the plugin and nothing else; the store is read by its tables.
- **Package**: the `opencode-deja` npm package ships `index.js` for 1.x and `server.js`, exported as `opencode-deja/server`, for 2.x. 2.x resolves that subpath first and wants a default `{ id, setup }`; 1.x from 1.3.4 resolves it too and calls its `server`; older 1.x loads `index.js`. In `opencode.json` it goes under `plugin` on 1.x and `plugins` on 2.x.
- **Resume**: `opencode -s <id>`, run in the session's directory, or from the current one when that directory is gone.

## Status line

opencode has no command status line, so `deja install opencode-auto` adds a
TUI plugin that runs `deja statusline` every 10 seconds and when the session
changes, and shows the first line. On 1.x it is `plugins/deja-status.tsx`,
listed in `tui.json` (1.x finds TUI plugins only there), and the line shows in
the sidebar, the prompt row and the home screen. On 2.x it is
`plugins/deja-status/tui.tsx`, which 2.x finds on its own, in the prompt and
home footers. A `tui.jsonc` is not edited; install says what to add. Rendered
live on 1.18.34 and 2.0.24.

## Known quirks and drift

- The database can be several gigabytes. deja projects JSON scalars in SQL instead of streaming complete blobs.
- Message content is split across `message` and `part`; one message can have several parts.
- Tool parts other than the ones above are ignored, and so is the output of `read`. A message is capped at 1 MiB.
- A missing database must not be passed to SQLite because the CLI would create it.
- The committed conformance fixture is SQL rather than a binary database; the test creates a temporary SQLite file.

**Last verified:** 2026-09-30
