# Claw Code

- **ID**: `clawcode`
- **Store**: `${CLAW_CONFIG_HOME:-~/.claw}/sessions/*.jsonl` and legacy `*.json`
- **Read override**: `DEJA_CLAWCODE_ROOT` or `DEJA_CLAWCODE_ROOTS`
- **Format**: JSONL transcripts (`session_meta`, `prompt_history`, `message` with `tool_use` / `tool_result` blocks) plus legacy JSON snapshots
- **Needs**: none

Claw Code writes append-only session JSONL transcripts and legacy JSON snapshots
under `${CLAW_CONFIG_HOME:-~/.claw}/sessions/`. The reader parses
`session_meta`, `prompt_history` timestamps (deduplicating against the user
`message` turn), and tool blocks (`read_file`, `write_file`, `edit_file`,
`bash`), while skipping `.meta.json` and `.deleted.json` sidecars.

- **Wiring**: `deja install clawcode` registers `mcpServers.deja` in
  `${CLAW_CONFIG_HOME:-~/.claw}/settings.json`, writes the skill at
  `${CLAW_CONFIG_HOME:-~/.claw}/skills/deja-history/SKILL.md`, and adds the
  `/deja` command at `${CLAW_CONFIG_HOME:-~/.claw}/commands/deja.md`.
- **Auto-recall**: `deja install clawcode-auto` wires `PreToolUse` (`deja hook-tool`),
  `PostToolUse` (`deja hook-tool-after`), and `PostToolUseFailure`
  (`deja hook-tool-after`) hooks into `${CLAW_CONFIG_HOME:-~/.claw}/settings.json`.
- **Resume**: `claw --resume <id>` from the recorded `workspace_root` directory.

**Last verified:** 2026-11-15
