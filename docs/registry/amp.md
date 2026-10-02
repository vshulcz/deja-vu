# Amp

Amp builds from 0.0.1774963753 (2026-03-31) on keep threads on ampcode.com
and write no thread files; the data directory holds only `bin`, `logs` and
`pids`. Reading those threads needs `amp threads export` and a login, which
deja does not do, so current Amp sessions are not indexed. When the data
directory is there and `threads/` holds nothing, `deja doctor` says so on the
amp row and in the store's `note` in `--json` (#4355).

Earlier builds stored one JSON object per thread under the local data
directory, and deja reads those:

- `${XDG_DATA_HOME:-~/.local/share}/amp/threads/`, on every OS: macOS and
  Windows use the same `~/.local/share` default, and a set `XDG_DATA_HOME` is
  followed there too.
- `DEJA_AMP_ROOT` replaces the thread directory directly. When set, it is the
  only root deja reads.

Each `*.json` file is one thread. The parser uses `id` as the stable session ID,
`title` as the session title, and the first `env.initial.trees[0].uri` when it
is a `file://` URI to derive the project name from its last two path segments,
as Claude Code does (#4457).
If that working-directory URI is absent or not a file URI, the project falls
back to the title.

A thread's `messages` are retained only for `user` and `assistant` roles. Within
each message, blocks with `type: "text"` are joined in order. `tool_use` blocks
become work records: `Bash` (`cmd`) a command, `Read`, `edit_file` and
`create_file` (`path`) the files touched, `edit_file`'s `old_str`/`new_str` an
edit span and the written side, `create_file`'s `content` the written side.
Models that get `shell_command` (`command`, `workdir`) and `apply_patch`
(`patchText`, the `*** Begin Patch` format) have those read too: a command, and
the patch's files, removed lines and added lines, relative paths under the
thread's workspace. A patch whose run ended `error`, `rejected-by-user` or
`cancelled` is not recorded (#4527). A `tool_result` block holds a `run`; its
`result.output` (or a string result, or the run's error) is kept as tool
output; a finished `Bash` run's
`result.exitCode` goes on the command as `→ exit N` (Amp's `-1`, no code from
the process, is left off) (#4530).

A user message is stamped with `meta.sentAt` (Unix milliseconds) and an
assistant message with `usage.timestamp` (ISO 8601). A message with neither,
such as a user turn holding only tool results, takes the previous message's
time, and the first one the thread's `created` time.

A malformed or truncated JSON file is reported as a per-file ingestion error
and skipped by discovery/load and index rebuilds; it does not prevent other
thread files from being indexed. Amp thread files are not append-only, so deja
uses the normal full-file parse path when a file changes.

## Synthetic fixture

[`fixtures/registry/amp/thread.json`](../../fixtures/registry/amp/thread.json)
is a minimal conformance sample with deterministic IDs, paths, and timestamps.
It contains no personal data or credentials.

**Last verified:** 2026-10-01
