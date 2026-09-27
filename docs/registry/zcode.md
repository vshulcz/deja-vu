# ZCode

- **ID**: `zcode`
- **Store**: `~/.zcode/projects/<encoded-cwd>/<session>.jsonl`
- **Store (CLI database)**: `~/.zcode/cli/db/db.sqlite` — OpenCode's schema
- **Read overrides**: `DEJA_ZCODE_ROOT` replaces the project root; `DEJA_ZCODE_DB` names the database
- **Format**: flat JSONL — one message per line
- **Needs**: `sqlite3` for the CLI database; the transcripts need nothing

ZCode is Z.ai's desktop agent and writes the same flat transcript Command Code
does, under the same project layout: `role`, `content`, `timestamp`,
`sessionId`, one message per line. Some lines also carry the API's `usage`
block, which deja has no use for and ignores.

**Last verified:** 2026-09-17

## Known quirks and drift

- **The CLI database is read on a third party's word.** It goes through
  OpenCode's schema reader (see below); that shape has not been checked
  against a running ZCode.
- Wiring: `deja install zcode` writes the server into `mcp.servers` in
  `~/.zcode/cli/config.json` — one level deeper than the `mcpServers` every
  other client here uses — and `deja install zcode-auto` adds the hooks to the
  same file, on `SessionStart` and `UserPromptSubmit`.
- **Three things decide whether that works, and all three are silent when
  wrong.** Config-file hooks do nothing without `hooks.enabled: true`. A
  config hook gets no template expansion, so the command carries an absolute
  path. And the output schema is strict: one key ZCode does not recognise and
  the whole response is discarded — which is why the installed line ends in
  `--strict`, dropping deja's receipt line and keeping the context.
- The shapes were not read from ZCode's own documentation, which does not
  describe them. They come from volcengine/OpenViking's memory plugin, whose
  `examples/agent-hook-plugin/DESIGN.md` records the surface it established by
  inspecting a live install — seven hook events, the manifest probe order, the
  strict schema — and ships an installer against it. Nothing here is verified
  on the machine deja was written on.

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
rather than a new reader: text parts, `read`, `bash` and `apply_patch` calls,
as the opencode page describes.

Said plainly: that is a third party's attestation, not a running ZCode checked
here. `task_type` also separates `subagent_child` and `fork` from `interactive`,
which is the distinction `DEJA_INCLUDE_SUBAGENTS` draws elsewhere — left alone
until there is a real store to measure it against, rather than guessed at
(#3675).
