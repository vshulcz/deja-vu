# Cline

- **ID**: `cline`
- **Stores** (two generations, one harness):
  - modern CLI/SDK: `${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/<sessionId>/<sessionId>.messages.json` with a `<sessionId>.json` manifest beside it
  - legacy VS Code extension (`saoudrizwan.claude-dev`): `<host globalStorage>/tasks/<taskId>/api_conversation_history.json` with `state/taskHistory.json` supplying title, cwd and timestamp; Code, Code Insiders, VSCodium, Cursor and Windsurf host roots are probed
- **Read overrides**: `DEJA_CLINE_ROOT` (modern sessions dir), `DEJA_CLINE_ROOTS` (path list of legacy extension roots)
- **Format**: whole-file JSON rewritten on change (not append-only) — full re-parse after atomic replacement, no incremental offsets. A change to the manifest alone, as `cline history update --title` makes, re-reads the session too (#4319), and so does a change to the task's own entry in `state/taskHistory.json` (#4446)

`user`/`assistant` turns are indexed for their text, from string content or
`type:"text"` blocks; thinking, images, compaction artifacts and non-lead agents
are skipped by design. Tool calls are read as well: `run_commands` becomes a
command record, the file tools a files record, and the editor's two sides the
replaced span and the hashed written lines. A tool result is indexed as tool
output whether it is a string or the CLI's list of
`{query, result, error, success}` entries that `run_commands` and
`read_files` write; an error the result does not already carry is kept with
it (#4315). A command carries `→ exit N` from its result: the entry's
`success` or its "Command exited with code N" error, or the extension's
"Command failed with exit code N." (#4502). The legacy extension's store takes
the Roo path for those, since its tools are Roo's — see
[Roo Code](roo.md) for the SEARCH/REPLACE shape and the workspace-relative
paths. The legacy `<task>...</task>` user envelope is unwrapped so the tags are
not indexed.

- **MCP**: `deja install cline` writes `mcpServers.deja` into
  `${CLINE_MCP_SETTINGS_PATH:-$CLINE_DATA_DIR/settings/cline_mcp_settings.json}`
  (flattened command/args shape, accepted by current Cline; existing entries
  preserved).
- **Auto, skill, command**: `deja install cline` also writes a plugin to
  `${CLINE_DIR:-~/.cline}/plugins/deja/`. It registers a rule whose content is
  session-start recall, a message builder that adds recall for each prompt and
  a repair after a failed command, the `/deja` command, and the
  `deja-history` skill bundled in the plugin. A `package.json` listing
  `index.js` under `cline.plugins` makes the directory a plugin package, which
  is the only way Cline loads its `skills/` (#4316). Cline's own hooks cannot
  carry context back, so the plugin is the channel.
- **Resume**: `cd <cwd> && cline --id <sessionId>` for modern sessions only;
  `cline --id` reopens the transcript from anywhere but runs its tools in the
  current directory, so the command runs in the manifest's `cwd` (#4318).
  Legacy VS Code tasks reopen from the extension UI.
- **Handoff**: `cline <prompt>` runs directly.

Specified by the community in
[#253](https://github.com/vshulcz/deja-vu/issues/253) with synthetic samples
and upstream source references.

**Last verified:** 2026-07-28
