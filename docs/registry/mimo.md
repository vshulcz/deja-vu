# MiMo-Code

- **ID**: `mimo`
- **Store**: `${MIMOCODE_HOME:-${XDG_DATA_HOME:-~/.local/share}/mimocode}/mimocode.db` (`MIMOCODE_DB` names the database file or path)
- **Read override**: `DEJA_MIMO_DB` replaces the store path
- **Format**: SQLite (OpenCode message schema)
- **Needs**: `sqlite3`

MiMo-Code (`XiaomiMiMo/MiMo-Code`, CLI `mimo`) is an OpenCode fork that keeps its
local conversation store in `mimocode.db` under `~/.local/share/mimocode` (or
`$XDG_DATA_HOME/mimocode` when set, overridden by `MIMOCODE_HOME` or
`MIMOCODE_DB`). Its tables — `session`, `message`, and `part`, or `session_v2`
and `session_message` — use OpenCode's message schema unchanged, so deja reads
and writes back sessions through the same SQLite reader (`parseOpencodeSchemaDB`
with harness `"mimo"`). See [OpenCode](opencode.md) for the field-by-field
schema description.

- **Wiring**: `deja install mimo` (alias `mimocode`) writes the `mcp.deja` block
  into `~/.config/mimocode/mimocode.jsonc` (or `mimocode.json` when that file is
  already present; `MIMOCODE_CONFIG_DIR` and `DEJA_MIMO_CONFIG_DIR` relocate the
  config directory), installs the skill at
  `~/.config/mimocode/skills/deja-history/SKILL.md`, and writes the `/deja`
  command to `~/.config/mimocode/commands/deja.md`.
- **Auto-recall**: `deja install mimo-auto` (alias `mimocode-auto`) adds
  `~/.config/mimocode/plugins/deja.js` and the TUI status plugin
  `~/.config/mimocode/plugins/deja-status.tsx` registered in `tui.json`.
- **Resume**: `mimo -s <id>` in the session's recorded directory.

**Last verified:** 2026-11-12
