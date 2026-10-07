# Kilo Code

- **ID**: `kilocode`
- **Store (extension)**: `<vscode-globalStorage>/kilocode.kilo-code/tasks/<taskId>/api_conversation_history.json` with `history_item.json` beside it — the Roo task shape, read across the same hosts Roo is (Code, Code - Insiders, VSCodium, Cursor, Windsurf)
- **Store (CLI)**: `~/.local/share/kilo/kilo.db`, or `$XDG_DATA_HOME/kilo/kilo.db` where that is set — OpenCode's message schema
- **Read overrides**: `DEJA_KILO_ROOTS` replaces the globalStorage list; `DEJA_KILO_DB` names the database
- **Format**: task JSON (Cline lineage) and SQLite (OpenCode schema)
- **Needs**: `sqlite3` for the CLI store; the task files need nothing

- **Wiring**: `deja install kilocode` writes the extension's
  `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json` for every host
  that has it, **and** the CLI's own `<config>/kilo/kilo.jsonc` (or `kilo.json` when that is the file present) — Kilo vendors
  OpenCode, so the CLI takes OpenCode's `mcp` block. `kilo mcp list` prints
  `✓ deja connected` once it is there. The skill goes in
  `~/.kilocode/skills/deja-history/SKILL.md` and the command in
  `<config>/kilo/commands/deja.md`.
- **Auto-recall**: `deja install kilocode-auto` does all of the above and adds
  `<config>/kilo/plugins/deja.js`. Kilo CLI kept opencode's plugin loader (it
  globs `{plugin,plugins}/*.{ts,js}` in each config directory), so the file is
  opencode-auto's plugin: the session digest folded into the system prompt,
  per-prompt recall, the file and failed-command lines after a tool, and the
  session-end stamp. It is always the opencode 1.x shape, whichever opencode is
  on PATH: Kilo 7.8.3 refuses the 2.x default export with "must default export
  an object with server()" (#4398).

Verified on `@kilocode/cli` 7.7.3. A real `kilo run` session landed in
`~/.local/share/kilo/kilo.db`, deja indexed it and recall returned the phrase
that was typed; `deja resume` printed `kilo -s <id>` and running it brought the
session back with that turn on screen. `kilo mcp list` prints `✓ deja
connected`, and all four surfaces are in its own palette at once:

```
/deja                 Search this machine's past AI coding sessions (deja-vu)
/deja:deja:mcp        Search this machine's past coding sessions — …
/deja-search:skill    deja-vu memory — search the user's past AI coding sessions …
/deja-history:skill   Search the user's past AI coding sessions. Use when …
```

Kilo namespaces its palette by kind — `:mcp` for a server's prompt, `:skill`
for a skill — so the command file, the server and both skills coexist under the
same word. Gemini's flat namespace is the opposite case and needed the command
file dropped there (#3665).

Verified on `@kilocode/cli` 7.8.3 with `deja install kilocode-auto`: a `kilo
run` in a project with one earlier Kilo session sent the model a system
message opening with the `<deja-recall>` digest naming that session, and the
reply answered from it.

**Last verified:** 2026-10-07

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
the project; without it the task's directory mtime is the base time. A
`history_item.json` written or changed after the transcript re-reads the task
(#4446). Tool calls are read as Roo's are (see [Roo Code](roo.md)), plus Kilo
Code's own: `search_and_replace` with an `operations` list of literal
`search`/`replace` pairs, `fast_edit_file` with `target_file` and a
`code_edit` whose `// ... existing code ...` lines are dropped before its
written lines are hashed, `write_file` (the alias of `write_to_file` kept in
history), and `delete_file` and `generate_image`, which name a file (#4535).

SQLite: `session` joined to `message` and `part`, exactly as OpenCode writes it —
see [OpenCode](opencode.md) for the field-by-field description. Kilo CLI adds
tools of its own, read beside OpenCode's: `background_process` gives a command
from `command` when its `action` is `start` or `monitor` (the other actions
name a process by `id`), `notebook_read` a file from `path`, and
`notebook_edit` a file from `path` and written lines from `source` when its
`action` is `insert` or `replace`. A notebook path relative to the session
directory is put under it (#4534).

## Status line

`deja install kilocode-auto` adds the opencode 1.x TUI plugin to Kilo CLI:
`plugins/deja-status.tsx` in `~/.config/kilo`, listed in its `tui.json`. It
runs `deja statusline` every 10 seconds and when the session changes, and
shows the line in the sidebar, the prompt row and the home screen. Rendered
live on Kilo CLI 7.8.8. The VS Code extension has no such surface.

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
- Skill location: `~/.kilocode/skills/deja-history/SKILL.md` is where Kilo's
  own loader looks first (`packages/opencode/src/kilocode/paths.ts`). A machine
  with the CLI and no editor gets the CLI config, the skill and the command,
  and a note saying no editor host carried the extension.
- The extension gets the same plugin. Kilo 7.x's VS Code extension starts its
  bundled CLI as `kilo serve` and leaves `XDG_CONFIG_HOME` alone, so the server
  loads `<config>/kilo/plugins/deja.js` like a terminal session does. Measured
  on VSIX 7.8.7 with a stub model: the digest reached the system prompt and the
  per-prompt recall the user turn of a session the extension's server ran.
