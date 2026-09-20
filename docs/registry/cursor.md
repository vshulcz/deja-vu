# Cursor session format

## Stores and files

Cursor IDE stores chats in `state.vscdb` under `globalStorage/` and `workspaceStorage/*/`. The user root is `~/Library/Application Support/Cursor/User` on macOS and `~/.config/Cursor/User` on other systems; an existing `$XDG_CONFIG_HOME/Cursor/User` is used when available. `DEJA_CURSOR_ROOT` overrides IDE discovery.

Cursor CLI writes `projects/<encoded-path>/agent-transcripts/**/*.jsonl` below `${CURSOR_CONFIG_DIR:-~/.cursor}`. `DEJA_CURSOR_CLI_ROOT` overrides transcript reads. Subagent transcripts are excluded unless `DEJA_INCLUDE_SUBAGENTS=1` — for index
size, not because the parent already holds that work. Unlike Claude's sidechain
files, Cursor's have not been read here: what the parent keeps and what only the
child has is unverified for this harness, so deja indexes them as it finds them
rather than deriving an edge it has not seen.

## IDE SQLite records

The `cursorDiskKV(key, value)` table has session values at `composerData:<id>` and message values at `bubbleId:<composer-id>:<bubble-id>`.

```json
{"composerId":"composer-7","name":"Fix cache invalidation","createdAt":1784278800000,"lastUpdatedAt":1784278802000}
{"type":1,"text":"Why is this stale?","timestamp":1784278801000,"workspaceProjectDir":"/work/api"}
```

Bubble `type: 1` maps to `user`; other numeric types map to `assistant`. Text uses `text`, falling back to `rawText`. Timestamps are Unix milliseconds. The first bubble with `workspaceProjectDir` supplies the project. Reading SQLite requires the `sqlite3` CLI.

## CLI JSONL records

```json
{"role":"assistant","message":{"content":[{"type":"text","text":"The cache key omitted the locale."}]}}
```

Tool calls follow the Anthropic `tool_use` shape with Cursor's own names: `path` rather than `file_path`, `Shell` rather than `Bash`, `old_string`/`new_string` on `StrReplace`, and the whole file as `contents` on `Write` — the last of these is the only record a created file's lines were ever in a session.

Only `user` and `assistant` roles are retained. Content follows the Anthropic string-or-parts shape. Control records such as `turn_ended` are ignored. The transcript has no message timestamps, so deja uses file modification time.

## The second CLI store, and why it is unread

Cursor CLI also writes one SQLite store per chat:

```
~/.cursor/chats/<workspace-hash>/<chat-uuid>/meta.json
~/.cursor/chats/<workspace-hash>/<chat-uuid>/store.db
```

`store.db` holds `blobs(id TEXT, data BLOB)` and `meta(key, value)`. The blobs are content-addressed and the tree needs no schema to walk: `meta`'s single value is hex-encoded JSON naming `latestRootBlobId`, that blob is protobuf whose repeated field 1 is a list of 32-byte child digests in message order, and each child is plain JSON in the Vercel AI SDK shape — `{"role","content"}` with `text`, `reasoning`, `tool-call` and `tool-result` parts, the calls named as above but keyed `toolName`/`args`.

deja does not read it, because on the machine where it was decoded reading it added nothing: all 19 stores walked, and of the 67 turns they held, 51 were already in the JSONL transcript and the remaining 16 were the `<user_info>` environment preamble the transcript omits. Every chat with a `store.db` had a transcript beside it. What `deja doctor` reports instead is the count of chats with no transcript beside them, which is silent today and is the only signal if a release stops writing `agent-transcripts`.

## Resume

CLI chats only. A transcript is named after the chat id `cursor-agent --resume`
takes, and the command runs in the project directory because Cursor lists chats
per workspace — deja prints `cd <project> && cursor-agent --resume <id>`, the
directory recovered from the encoded path (Cursor writes it without the leading
separator, `Users-x-app`). Live-verified: the resumed chat answered from its own
history. IDE chats carry a composer id from `state.vscdb` that the CLI does not
take, so those still reopen only in the editor.

## Known quirks and drift

- Cursor moved modern IDE chats toward global storage while older versions used workspace databases; both are scanned.
- SQLite values are JSON inside a key-value table and malformed or null entries occur.
- CLI project path encoding has the same separator-versus-hyphen ambiguity as Claude Code.
- SQLite text is capped at 64 KiB. CLI subagent duplication is opt-in.
- Both CLI layouts are written by `cursor-agent 2026.09.02-c22c1a3`, minutes apart in the same session.

**Last verified:** 2026-09-21
