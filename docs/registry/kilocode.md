# Kilo Code

- **ID**: `kilocode`
- **Store (extension)**: `<vscode-globalStorage>/kilocode.kilo-code/tasks/<taskId>/api_conversation_history.json` with `history_item.json` beside it — the Roo task shape, read across the same hosts Roo is (Code, Code - Insiders, VSCodium, Cursor, Windsurf)
- **Store (CLI)**: `~/.local/share/kilo/kilo.db`, or `$XDG_DATA_HOME/kilo/kilo.db` where that is set — OpenCode's message schema
- **Read overrides**: `DEJA_KILO_ROOTS` replaces the globalStorage list; `DEJA_KILO_DB` names the database
- **Format**: task JSON (Cline lineage) and SQLite (OpenCode schema)
- **Needs**: `sqlite3` for the CLI store; the task files need nothing

**Last verified:** 2026-09-16

Kilo Code is a Roo Code fork that vendors OpenCode — `packages/opencode` is
1,780 files inside the Kilo repository — and `packages/kilo-vscode/src/legacy-migration`
reads the task directory to import it into the database. So the task files are
the history of anyone who used Kilo before that migration, and the database is
where it goes afterwards; deja reads both and neither needed a new parser.

The extension id is `kilocode.kilo-code`, confirmed from
`packages/kilo-vscode/package.json` (`publisher: kilocode`, `name: kilo-code`).

Task JSON: one document per task, an array of `{role, content}` turns in Cline's
shape, with the user's first turn wrapped in `<task>…</task>`. `history_item.json`
supplies the id, the millisecond timestamp and the workspace, which is what names
the project; without it the task's directory mtime is the base time.

SQLite: `session` joined to `message` and `part`, exactly as OpenCode writes it —
see [OpenCode](opencode.md) for the field-by-field description.

## Known quirks and drift

- Resume: `kilo -s <id>` for a session from the CLI store — "session id to
  continue" in Kilo's own CLI options (`packages/opencode/src/cli/cmd/tui.ts`).
  An editor task is refused with the reason: those live under the host's
  globalStorage and reopen from Kilo's history view, the same split Roo has.
- Two stores for one harness, and a session can exist in both if the migration
  has run: the task files are not deleted by it.
- A Roo task and a Kilo task are the same file name in sibling directories under
  one globalStorage, so the reader matches on the extension id rather than on the
  file.
- tokscale's Kilo reader notes that an assistant turn whose payload carries no
  `time` object falls back to the file's mtime. deja sorts by time rather than
  counting tokens, so the turn still needs a time; the OpenCode path already
  falls back the same way.
- The slash command is real after all: Kilo's own workflows doc puts global
  commands in `~/.config/kilo/commands/`, project ones in `.kilo/commands/`,
  and a file named `deja.md` is invoked as `/deja`. `deja install kilocode`
  writes the global one.
- Wiring: `deja install kilocode` writes the MCP server into
  `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json` for every host that carries the
  extension, and the shared manual into `~/.kilocode/skills/deja-search/SKILL.md`, which is where
  Kilo's own loader looks first (`packages/opencode/src/kilocode/paths.ts`). A machine with the CLI
  and no editor gets the skill and a note saying the server was not wired anywhere.
- No hooks: a search of Kilo-Org/kilocode finds no hook surface, and the extension is a Roo fork
  whose hooks are still in flight upstream, so recall arrives when the model calls the tool rather
  than on its own.
