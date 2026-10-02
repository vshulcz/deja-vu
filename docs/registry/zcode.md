# ZCode

- **ID**: `zcode`
- **Store**: `~/.zcode/projects/<encoded-cwd>/<session>.jsonl`
- **Store (CLI database)**: `~/.zcode/cli/db/db.sqlite` — OpenCode's schema
- **Store (legacy snapshots)**: `~/.zcode/v2/sessions/<workspaceHash>/<taskId>.json`
- **Read overrides**: `DEJA_ZCODE_ROOT` replaces the project root; `DEJA_ZCODE_DB` names the database; `DEJA_ZCODE_LEGACY_ROOT` replaces the snapshot root
- **Format**: flat JSONL — one message per line
- **Needs**: `sqlite3` for the CLI database; the transcripts need nothing

ZCode is Z.ai's desktop agent and writes the same flat transcript Command Code
does, under the same project layout: `role`, `content`, `timestamp`,
`sessionId`, one message per line. Some lines also carry the API's `usage`
block, which deja has no use for and ignores.

**Last verified:** 2026-09-17

## Known quirks and drift

- **The CLI database is OpenCode's schema with Claude Code's tool names.**
  It goes through OpenCode's schema reader (see below), checked against a
  store the ZCode 3.14.4 runtime wrote, whose tool parts needed their own
  names read (#4428).
- Wiring: `deja install zcode` writes the server into `mcp.servers` in
  `~/.zcode/cli/setting.json` — one level deeper than the `mcpServers` every
  other client here uses — and `deja install zcode-auto` adds the hooks to the
  same file, under `hooks.events.SessionStart` and
  `hooks.events.UserPromptSubmit`. The runtime (3.14.4) writes that file on
  its first launch and reads it from then on. deja used to write
  `config.json`, which the runtime reads once, as the source of that
  first-launch migration, so nothing reached the agent on a machine where
  ZCode had run; an uninstall clears deja's entries from both. Before that
  first launch, install starts `setting.json` from `config.json` the way the
  runtime would, since the runtime skips its migration once the file exists,
  and `deja doctor` reads hooks outside `hooks.events` or with
  `hooks.enabled` off as stale (#4429).
- **Three things decide whether that works, and all three are silent when
  wrong.** Config-file hooks do nothing without `hooks.enabled: true`, and
  the runtime's own `setting.json` starts with it off: install turns it on and
  says so, and uninstall puts back what was there, so hooks the reader had
  switched off do not stay on (#4431). A
  config hook gets no template expansion, so the command carries an absolute
  path. And the output schema is strict: one key ZCode does not recognise and
  the whole response is discarded — which is why the installed line ends in
  `--strict`, dropping deja's receipt line and keeping the context.
- The shapes were not read from ZCode's own documentation, which does not
  describe them. They come from volcengine/OpenViking's memory plugin, whose
  `examples/agent-hook-plugin/DESIGN.md` records the surface it established by
  inspecting a live install — seven hook events, the manifest probe order, the
  strict schema — and ships an installer against it. The file and the
  nesting have moved since; the three rules above held on the 3.14.4 runtime,
  where the hooks fired and the digest reached the request (#4429).
- `deja resume` on a session from the CLI database prints
  `cd '<dir>' && zcode --resume <sess_id>`, the directory from the session
  row. The terminal client reopens the session from any directory; the `cd`
  keeps the agent in the project. A JSONL transcript has no such command
  (#4430).

## The CLI database

It was left unread while nothing said what shape it was in. deja reads it
now, on the shape attested by `zcode-stats` 0.8.0 — a read-only dashboard over the live database,
whose own description names `~/.zcode/cli/db/db.sqlite` and whose queries name
the tables:

```sql
SELECT directory, count(*), sum(task_type = 'interactive'),
       sum(task_type = 'subagent_child'), sum(task_type = 'fork'),
       max(time_updated) FROM session GROUP BY directory
SELECT json_extract(data, '$.role') FROM message GROUP BY role
SELECT json_extract(data, '$.type') FROM part GROUP BY type
```

`session` / `message(data)` / `part(data)` is OpenCode's schema, which deja
already parses for OpenCode and for Kilo Code's CLI, so this is one more root
rather than a new reader. The tool parts are not opencode's, though: a store the
ZCode 3.14.4 runtime wrote names them as Claude Code does, with its arguments —
`Bash {command}`, `Read {file_path}`, `Edit {file_path, old_string,
new_string}`, `Write {file_path, content}`. deja reads those for ZCode as
commands with their output, files, edit spans and written lines; an `Edit`
ZCode refused (`state.status: "error"`) is not recorded as a change (#4428).
A Bash part has no `metadata.exit`; a failed run's output opens with "Exit code
N", as Claude Code's does, and that line goes on the command as `→ exit N`
rather than into the output (#4536).

Said plainly: that is a third party's attestation, not a running ZCode checked
here. `task_type` also separates `subagent_child` and `fork` from `interactive`,
which is the distinction `DEJA_INCLUDE_SUBAGENTS` draws elsewhere — left alone
until there is a real store to measure it against, rather than guessed at
(#3675).

## Legacy snapshots

Before the current runtime ZCode kept each conversation as one JSON file,
`~/.zcode/v2/sessions/<workspaceHash>/<taskId>.json`:

```json
{"meta": {"taskId": "…", "acpSessionId": "…", "workspacePath": "/path/to/proj",
          "title": "…", "createdAt": 1790000000000, "updatedAt": 1790000060000},
 "messages": [{"role": "user", "content": "…", "timestamp": 1790000000000}]}
```

That is the shape the 3.14.4 runtime's own restore-legacy-sessions skill scans
(`scan-legacy-sessions.mjs`). The files stay until the user restores them by
hand, so deja reads them: `acpSessionId`, else `taskId`, is the id, the one a
restore gives the session, `workspacePath` names the project, and the user and
assistant text is indexed. Files ending `.deleted.json` are skipped, as ZCode
skips them, and a snapshot whose id is already in the CLI database is read
from there instead, from the first pass after the restore on (#4448). `deja resume` on a snapshot names ZCode's
`/restore-legacy-sessions` command rather than a `zcode --resume` that would
not find it (#4432).
