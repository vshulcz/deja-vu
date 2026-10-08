# Reasonix

- **ID**: `reasonix`
- **Store (1.x)**: `<state>/projects/<workspace-slug>/sessions-v4/<id>/events.frames` per workspace, `<state>/sessions-v4/<id>/events.frames` for CLI hosts with no workspace, and `<state>/desktop-sessions-v5/by-id/<id>/events.frames` for the desktop app
- **Store (JSONL)**: `<state>/projects/<workspace-slug>/sessions/<id>.jsonl` per workspace, and `<state>/sessions/<id>.jsonl` for sessions with none
- **State root**: `$REASONIX_STATE_HOME`, else `$REASONIX_HOME`, else a `[storage] state` entry in `<home>/config.toml`, else `~/.reasonix` on macOS and Linux and `%APPDATA%\reasonix` on Windows
- **Store (legacy)**: `~/.reasonix` on Windows, and the OS config directory (`~/Library/Application Support/reasonix`, `$XDG_CONFIG_HOME/reasonix`, `~/.config/reasonix`) on macOS and Linux — read while they are on disk, unless `REASONIX_HOME`, `REASONIX_STATE_HOME` or `DEJA_REASONIX_ROOT` is set
- **Read overrides**: `DEJA_REASONIX_ROOT` replaces the state root; pointed at a `sessions` directory it reads that directory alone
- **Format**: 1.x — an event log of zstd frames; JSONL — one message per line, no envelope
- **Needs**: the `zstd` CLI for 1.x sessions; nothing for JSONL
- **Resume**: `reasonix --resume <id>`, run in the workspace the session was worked in; a JSONL session saved with no workspace, or whose workspace is gone, is resumed by its file path from anywhere, and a 1.x session whose workspace is gone is refused with `deja show` (#4459). 1.x sessions in the global or desktop store have no resume command

Reasonix is a Go coding agent built around DeepSeek's prefix cache. The
layout above comes from its own resolver, `internal/contract/config`
(`storage_roots.go`, `paths.go`); the workspace slug is the absolute path with
separators and the drive colon turned into dashes, lower-cased on Windows.

A line is `{role, content, …}` with `role` one of `system`, `user`,
`assistant`, `tool`. `content` is a string. An assistant turn carries
`tool_calls: [{id, name, arguments}]`, where `arguments` is a JSON string; the
v0.x builds wrote OpenAI's nested `{id, type, function: {name, arguments}}`
instead, and both are read. `bash` becomes a command record, `read_file`,
`write_file`, `edit_file` and `multi_edit` become file records, and the edits
keep the replaced span. `notebook_edit`, `delete_range` and `delete_symbol`
name their file under `path` and `move_file` two, under `source_path` and
`destination_path`; `notebook_edit`'s `new_source` is the written side unless
its `edit_mode` is `delete`, and `delete_range`, which names only anchors, has
its removed lines read from the unified diff its result returns — a call that
failed returns an error and gives none (#4541). A `tool` line is tool output. `system` lines and user
lines marked `host_authored` are dropped: the host wrote them, not the person.
Where `raw_content` is set on a user or tool line it is the text before the
host added context or cut a result for the model, and that is what is indexed.

Beside each transcript sits `<id>.jsonl.meta`, which carries `created_at`,
`updated_at`, `workspace_root` and the titles (`custom_title`, `topic_title`,
`name`). The session's clock and project come from there, and a rename that
rewrites only this file re-reads the session (#4446).

## 1.x session directories

Reasonix 1.x (npm `reasonix` 1.x, branch `main-v2`) keeps each session as a
directory: `manifest.json` (`sessionId`, `createdAt`, `codec`
`reasonix.session.linear/v4`), `header.json` with the workspace as `cwd` when
the desktop app made the session, and `events.frames`. The rest —
`events.offset-index.json`, `storage.identity.json`, `writer.lock` — and the
dot directories beside the sessions (`.query-cache`, `.recovery-cache`,
`.content-v1`) are not transcripts.

`events.frames` is a run of frames: `RX4F`, the compressed and the raw size as
big-endian uint32, then one zstd frame holding a JSON record
(`internal/session/v4_codec.go`). Records come in batches — `batch/begin`,
the events, `batch/end` with the SHA-256 of the records before it — and only a
finished batch counts: a trailing batch with no end is a write in progress.
An event's `payload` is base64 JSON; a payload over 64 KiB is a `payloadRef`
to `.content-v1/objects/<aa>/<bb>/<sha256>` beside the sessions.

The message list is replayed the way Reasonix projects it:
`message/complete` appends `{message}`, `message/upsert` replaces by message
id, `message/retract` drops `messageIds`, and `history/replace` and
`legacy/import` swap in a whole list. `session/title` is the title. A message
is Reasonix's provider message: `origin` is `user` or `host`, and host
messages (the session-context snapshot) and `local_only` records are dropped;
`createdAt` is unix milliseconds on user turns, and other messages take their
batch's time; `tool_calls` and `raw_content` read as in the JSONL store, and a
`tool` result's `tool_execution.exitCode` rides on its command when non-zero.

The workspace is `header.json`'s `cwd`, else the `Current workspace: "<root>"`
line of the host's session-context message (the CLI writes no header), else,
for a desktop session, the workspace that lists its id in
`<state>/desktop/workspace-state-v1.json`.

## Wiring

Reasonix takes everything as one plugin package (`reasonix-plugin.json`,
`apiVersion: reasonix.io/plugin/v2`). deja writes the package to
`~/.config/deja/reasonix-plugin` and hands it to
`reasonix plugin install <dir> --name deja --replace --yes`, which copies it to
`<home>/plugins/deja` and records it in `<home>/plugin-packages.json`. With no
`reasonix` on PATH deja writes the same layout itself. `<home>` is
`$REASONIX_HOME`, else `~/.reasonix` on macOS and Linux and
`%APPDATA%\reasonix` on Windows. `config.toml` is not touched.

- **MCP**: `deja install reasonix` puts the server in the package's
  `mcpServers`. Package servers start with the session; the model reaches
  `deja` through `use_capability`.
- **Skill**: `skills/deja-history/SKILL.md` in the package.
- **Command**: `commands/deja.md` in the package, listed as `/deja:deja`.
- **Auto-recall**: `deja install reasonix-auto` adds a runtime, `deja
  reasonix-ext`, speaking Reasonix's extension protocol v2 (JSON-RPC 2.0 over
  stdio). Hooks cannot carry it: on 1.39.1 only `SessionStart` stdout reaches
  the model. The extension appends the session digest (first turn) and the
  per-prompt recall to the user turn at `input.receive`, so recall sits in the
  turn tail and the prefix cache is not disturbed; the system prompt is never
  edited. Recall is asked about what the person typed: the host's own blocks,
  the plan-mode marker and `Referenced context:` file bodies are left out of
  the query, and the host's own turns (a goal round, the message after a plan
  approval) get nothing. Reasonix stores the typed text as `raw_content`, and
  that is what deja indexes. At `tool.after` it adds the pre-tool line for a
  shell command (`bash`, or `pwsh` and `powershell` on Windows) or a file
  write — `write_file`, `edit_file`, `multi_edit`, `notebook_edit`,
  `delete_range`, `delete_symbol`, and `move_file` under the file it moved
  from (#4541) — and when a command failed, what fixed the same failure before. Both
  go after the output; for output Reasonix will cut to a CI summary (first
  and last eight lines) they go in front, on one line. At
  `compaction.prepare` it adds deja's record of the folded turns, read from
  what the person typed rather than the recall appended to it, to the
  summarizer's guidance, and holds the same packet for the next turn, which
  carries it in place of that turn's recall. It publishes a status line while the first index
  builds and one "recalled N prior sessions" notice per session. Every answer
  has a budget under the host's timeout (4 s per prompt, 2 s per tool call,
  10 s for compaction); an error, a timeout or nothing to say leaves the turn
  as the host built it.
- **Statusline**: not installed. A Reasonix statusline replaces the built-in
  row and lives in `config.toml`; the extension's status and notice lines
  carry the same information.
- **Trust**: a runtime runs with full trust. `--yes` is the approval, and
  install prints what was trusted; `reasonix plugin show deja` lists the
  intercepts.
- **Uninstall**: removes the package directory and deja's record, and the
  state file too when deja created it. A `deja` package deja did not write —
  a linked one (`plugin install --link`), or a record pointing at another
  source — is left alone, and install refuses to replace it. When install
  ran `reasonix`, the receipt-signing key and crash directory that run
  created go too, unless Reasonix has used the key since.
- **Resume**: `reasonix --resume <id>` in the session's workspace.
- **Handoff**: exec, `reasonix run <prompt>`; the TUI takes no prompt.

**Last verified:** 2026-09-27

## Known quirks and drift

- **Per-message time is partial.** The host stamps `createdAt` (unix
  milliseconds) on the turns it records; tool results carry none. An unstamped
  line sits one millisecond after the line before it, so two identical turns
  stay two records (#3333).
- **v0.x kept the clock elsewhere.** Its `<id>.meta.json` holds only
  `workspace` and `summary`, and the timestamps are `ts` in
  `<id>.events.jsonl`. With neither sidecar present the file's mtime is used.
- **Legacy copies.** Reasonix imports legacy sessions into its current store
  and leaves the originals, so a legacy transcript is read only when the
  current store has no file with its name.
- **Copies between stores.** 1.x mirrors a JSONL transcript into
  `sessions-v4` under the same id, migrates one under a new id naming the
  original in the manifest's `source.path`, and the desktop imports sessions
  into its own store, recording each source under `sourceMappings` in
  `workspace-state-v1.json`. None of them deletes the original. deja reads the
  newest store's copy: desktop, then `sessions-v4`, then JSONL. A fork also
  names its parent in `source.path`; that is a directory, and the parent is
  still read.
- **Resume in 1.x.** `--resume <id>` looks in the `sessions-v4` store whose
  slug is the working directory's, not the git root the session-context names,
  so the directory deja prints is the one whose slug matches the store.
  The global and desktop stores are not searched by `--resume`.
- **Sidecars share the directory.** `.events.jsonl`, `.wire.jsonl`,
  `.guardian.jsonl`, `.conflicts.jsonl`, `.adjudication.jsonl`,
  `.execution.jsonl` and `.turns.jsonl` end in `.jsonl` and are not
  transcripts; nor are flat `subagent-*.jsonl` worker logs or the `subagents/`
  tree. Locks, metadata, context and recovery files sit there too.
- **Where the shapes come from.** The JSONL store is read from Reasonix's own
  code (`internal/state/store/session.go`, `internal/state/sessionstore`,
  `internal/contract/provider/provider.go`) and from the transcript sample in
  #4053, not from a running install. The 1.x store is read from the 1.x code
  (`internal/session`, `internal/sessioncontent`, `internal/config/paths.go`)
  and was checked against a
  live `reasonix` 1.39.1: sessions it wrote are read here, and it resumes the
  1.x fixture deja checks in.
- **Pre-tool lines arrive with the result.** A `tool.before` answer can
  only let a call run, change it or stop it, so the line deja has about a
  command or file is asked for at `tool.after`, which fires only for a call
  that ran, and added to that call's result.
- **Which session.** The extension is told a host-local id (`boot-1` on
  1.39.1). A `session.start` or `session.load` event names the session in
  `sessionPath` where Reasonix has one — a bare id on the 1.x binding, a
  session directory or a JSONL path elsewhere — and that is the key; a
  `session.rotate` names the session that is ending. Until an event names
  it, deja files the session under a key of its own. A session event with no
  name that arrives within two seconds of a turn is taken as that turn's own.
- **2.x.** Reasonix 2.30 speaks the same v2 protocol and was run live: it
  sends `session.start` or `session.load` with the JSONL path ahead of every
  `input.receive`, and keeps sessions in `projects/<slug>/sessions/`, with no
  `sessions-v4`.
