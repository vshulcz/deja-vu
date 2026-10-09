# Cursor session format

## Stores and files

Cursor IDE stores chats in `state.vscdb` under `globalStorage/` and `workspaceStorage/*/`. The user root is `~/Library/Application Support/Cursor/User` on macOS, `%APPDATA%\Cursor\User` on Windows and `~/.config/Cursor/User` elsewhere; on Linux an existing `$XDG_CONFIG_HOME/Cursor/User` is used when available. `DEJA_CURSOR_ROOT` overrides IDE discovery.

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

Only `user` and `assistant` roles are retained. Content follows the Anthropic string-or-parts shape. Control records such as `turn_ended` are ignored. Records carry no timestamp field; a user turn's text starts with a `<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>` block, which deja reads and carries forward to the assistant turns that answer it. A transcript with no readable block falls back to the file's modification time.

## The second CLI store

Cursor CLI also writes one SQLite store per chat:

```
~/.cursor/chats/<workspace-hash>/<chat-uuid>/meta.json
~/.cursor/chats/<workspace-hash>/<chat-uuid>/store.db
```

`store.db` holds `blobs(id TEXT, data BLOB)` and `meta(key, value)`. The blobs are content-addressed and the tree needs no schema to walk: `meta`'s single value is hex-encoded JSON naming `latestRootBlobId`, that blob is protobuf whose repeated field 1 is a list of 32-byte child digests in message order, and each child is plain JSON in the Vercel AI SDK shape — `{"role","content"}` with `text`, `reasoning`, `tool-call` and `tool-result` parts, the calls named as above but keyed `toolName`/`args`.

Each chat also has a `meta.json` beside its `store.db` holding the `cwd` it ran in, and the `<workspace-hash>` above it is md5 of that path; deja reads it for the project name and the resume directory (#4193). The transcript holds every turn the store does except the `<user_info>` environment preamble, but none of the tool results, so deja reads the store for those alone: each transcript call is paired with the store's call of the same tool name and arguments, in order, and takes its result. A Shell result gives the exit status on the command and the printed output without Cursor's `Exit code` / `Command output` framing; the results of `GetMcpTools` and `GetDynamicTools`, which are the MCP servers' own descriptions, are skipped. Every chat with a `store.db` had a transcript beside it, and `deja doctor` reports the count of chats with none — silent today, and the only signal if a release stops writing `agent-transcripts`.

## Wiring

`deja install cursor` adds the server to `${CURSOR_CONFIG_DIR:-~/.cursor}/mcp.json`, writes the shared skill `~/.agents/skills/deja-history/SKILL.md` and the `/deja` command in `commands/deja.md` beside it. `deja install cursor-auto` adds the same plus `hooks.json` entries: `sessionStart` (`deja hook-context`), `beforeSubmitPrompt` (`hook-prompt`, interactive TUI only — headless `-p` skips it), `preToolUse` (matcher `^(Shell|Write|Task)$`), `postToolUse` and `postToolUseFailure` (matcher `^Shell$`; the second fires for a tool that errors, with the text in `error_message`), `preCompact` and `sessionEnd` (`hook-session-end`, so the session you closed is back in the next one's MCP recall). Cursor also runs the hooks in `~/.claude/settings.json` and dedupes them against its own by exact command string, so a machine with both wired gets one injection. Cursor tests a matcher as a regex against `tool_name` for the three tool events (2026.09.02), so deja does not start on every read and grep.

## Resume

CLI chats only. A transcript is named after the chat id `cursor-agent --resume`
takes, and the command runs in the project directory because Cursor lists chats
per workspace — it looks a chat up under `chats/<md5 of the cwd>/<id>` — so deja
prints `cd <project> && cursor-agent --resume <id>`, the directory read from the
chat's `meta.json`, whose folder is that md5. The encoded transcript folder is
only the fallback: it blanks a dot, a space or a non-ASCII character and cuts a
long path with a hash, so it cannot be read back for those. A chat whose
directory is gone, is not recorded, or is no longer in cursor-agent's store is
refused with `deja show` instead — `cursor-agent --resume` with no id lists
chats from every directory. Live-verified: the resumed chat answered from its own
history. IDE chats carry a composer id from `state.vscdb` that the CLI does not
take, so those still reopen only in the editor. On the screen bare `deja`
opens, `r` on a CLI chat runs this command for you, and `o` continues any chat
in another agent.

## Status line

`deja install cursor-auto` sets `statusLine` in `cli-config.json` to
`{"type":"command","command":"<deja> statusline"}`. cursor-agent reads that file
from `$CURSOR_CONFIG_DIR`, then `$XDG_CONFIG_HOME/cursor` (on macOS too), then
`~/.cursor`. It runs the command without a shell and pipes Claude's payload,
`transcript_path` included, after each turn; there is no timer. A status line
already set there is left alone. Rendered live on cursor-agent 2026.09.02.

## Known quirks and drift

- Cursor moved modern IDE chats toward global storage while older versions used workspace databases; both are scanned.
- SQLite values are JSON inside a key-value table and malformed or null entries occur.
- CLI project path encoding has the same separator-versus-hyphen ambiguity as Claude Code.
- A parsed message is capped at 1 MiB. CLI subagent transcripts are opt-in.
- Both CLI layouts are written by `cursor-agent 2026.09.02-c22c1a3`, minutes apart in the same session.

**Last verified:** 2026-10-07
