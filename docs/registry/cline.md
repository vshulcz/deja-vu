# Cline

- **ID**: `cline`
- **Stores** (two generations, one harness):
  - modern CLI/SDK: `${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/<sessionId>/<sessionId>.messages.json` with a `<sessionId>.json` manifest beside it
  - legacy VS Code extension (`saoudrizwan.claude-dev`): `<host globalStorage>/tasks/<taskId>/api_conversation_history.json` with `state/taskHistory.json` supplying title, cwd and timestamp; Code, Code Insiders, VSCodium, Cursor and Windsurf host roots are probed
- **Read overrides**: `DEJA_CLINE_ROOT` (modern sessions dir), `DEJA_CLINE_ROOTS` (path list of legacy extension roots)
- **Format**: whole-file JSON rewritten on change (not append-only) — full re-parse after atomic replacement, no incremental offsets

Only `user`/`assistant` turns with string content or `type:"text"` blocks are
indexed. Tool payloads, thinking, files, images, compaction artifacts and
non-lead agents are skipped by design. The legacy `<task>...</task>` user
envelope is unwrapped so the tags are not indexed.

- **MCP**: `deja install cline` writes `mcpServers.deja` into
  `${CLINE_MCP_SETTINGS_PATH:-$CLINE_DATA_DIR/settings/cline_mcp_settings.json}`
  (flattened command/args shape, accepted by current Cline; existing entries
  preserved).
- **Resume**: `cline --id <sessionId>` for modern sessions only; legacy VS
  Code tasks reopen from the extension UI.
- **Handoff**: paste.

Specified by the community in
[#253](https://github.com/vshulcz/deja-vu/issues/253) with synthetic samples
and upstream source references.
