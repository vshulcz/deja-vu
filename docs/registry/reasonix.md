# Reasonix

- **ID**: `reasonix`
- **Store**: `<state>/projects/<workspace-slug>/sessions/<id>.jsonl` per workspace, and `<state>/sessions/<id>.jsonl` for sessions with none
- **State root**: `$REASONIX_STATE_HOME`, else `$REASONIX_HOME`, else a `[storage] state` entry in `<home>/config.toml`, else `~/.reasonix` on macOS and Linux and `%APPDATA%\reasonix` on Windows
- **Store (legacy)**: `~/.reasonix` on Windows, and the OS config directory (`~/Library/Application Support/reasonix`, `~/.config/reasonix`) on macOS and Linux — read while they are on disk, unless `REASONIX_HOME` or `REASONIX_STATE_HOME` is set
- **Read overrides**: `DEJA_REASONIX_ROOT` replaces the state root; pointed at a `sessions` directory it reads that directory alone
- **Format**: JSONL — one message per line, no envelope
- **Needs**: nothing
- **Resume**: `reasonix --resume <id>`, run in the workspace the session was worked in; a session saved with no workspace is resumed by its file path

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
keep the replaced span. A `tool` line is tool output. `system` lines and user
lines marked `host_authored` are dropped: the host wrote them, not the person.
Where `raw_content` is set on a user or tool line it is the text before the
host added context or cut a result for the model, and that is what is indexed.

Beside each transcript sits `<id>.jsonl.meta`, which carries `created_at`,
`updated_at`, `workspace_root` and the titles (`custom_title`, `topic_title`,
`name`). The session's clock and project come from there.

**Last verified:** 2026-09-26

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
- **Sidecars share the directory.** `.events.jsonl`, `.wire.jsonl`,
  `.guardian.jsonl`, `.conflicts.jsonl`, `.adjudication.jsonl`,
  `.execution.jsonl` and `.turns.jsonl` end in `.jsonl` and are not
  transcripts; nor are flat `subagent-*.jsonl` worker logs or the `subagents/`
  tree. Locks, metadata, context and recovery files sit there too.
- **The shapes come from the source, not from a running install.** Everything
  above is read from Reasonix's own code (`internal/state/store/session.go`,
  `internal/state/sessionstore`, `internal/contract/provider/provider.go`) and
  from the transcript sample in #4053. Nothing here has been checked against a
  live Reasonix on this machine.
- **Nothing is wired.** deja reads this store and writes nothing into
  Reasonix.
