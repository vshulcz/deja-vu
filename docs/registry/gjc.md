# gajae-code

- **ID**: `gjc`
- **Store**: `${GJC_CODING_AGENT_DIR:-~/.gjc/agent}/sessions/<project-slug>/<session>.jsonl`
- **Sub-agent passes**: `…/<project-slug>/<session>/N-*.jsonl`
- **Read override**: `DEJA_GJC_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

gajae-code (`gjc`) is a pi descendant: a `session` header carrying the id and
the cwd, then one `message` line per turn, so the parsing is pi's. The encoded
project directory names the project, and the header's cwd wins when it is there.

**Last verified:** 2026-09-17

## Known quirks and drift

- Resume: `gjc --resume <id>`. From its session-operations document:
  `--resume <id|path>` at startup opens an existing session, and a session
  belonging to another project forks into the current one — so deja prints
  the command without a working directory rather than guessing at one.
- **Sub-agent passes sit one directory deeper**, under a directory named for the
  session they belong to, one file per pass. They are skipped: a sub-agent's
  transcript repeats the parent's work in its own words, and indexed as a
  session of its own it competes with the parent for the same recall slot.
  `DEJA_INCLUDE_SUBAGENTS=1` takes them, the switch Claude Code's and Cursor's
  sub-agents already use.
- `service_tier_change` lines are not turns and are dropped rather than read as
  empty messages.
- Wiring: `deja install gjc` writes the server into `~/.gjc/agent/mcp.json`
  and the skill into `~/.gjc/agent/skills/deja-history/SKILL.md`. Both paths
  are from gjc's own surface table (`docs/customization.md`), and the skill
  location matters: gjc loads its native skills directory, while Claude's and
  Codex's are import candidates it does not read, so a skill written there
  would be a file no session ever sees.
- Auto-recall is `deja install gjc-auto`, and it is pi's extension unchanged:
  `loadExtensionModules` in `src/discovery/builtin.ts` discovers modules in
  `<agent dir>/extensions` (native `.gjc`/`.pi` only, a `*.ts` file or a
  directory with an index), and the events in
  `src/extensibility/extensions/types.ts` are pi's — `session_start`,
  `before_agent_start`, `context`, `tool_result`, `session_compact` — with the
  same result shapes.
- The directory hooks stay unwired, and not for want of a path. gjc's two
  documents disagree on it (`~/.gjc/hooks/{pre,post}` against
  `~/.gjc/agent/hooks/{pre,post}`), and the loader settles it:
  `resolveScopePaths` puts the user-scope hooks at `<agent dir>/hooks/<pre|post>`,
  so `docs/hooks.md`'s `~/.gjc/hooks` is stale. What stops a hook being written
  there is the shape: a directory hook is a module exporting
  `default (api) => api.on("tool_call", …)` whose only documented return is
  `{block, reason}` — allow or refuse, with no channel for adding context. The
  extension has that channel, so that is where recall goes.

## Measured on a live install

`gajae-code` 0.17.1 (which wraps `@gajae-code/coding-agent`), in a hermetic
HOME:

- `deja install gjc` writes the three paths this entry claims:
  `~/.gjc/agent/mcp.json`, `~/.gjc/agent/skills/deja-history/SKILL.md` and
  `~/.gjc/agent/commands/deja.md`.
- gjc's own `--help` confirms the MCP path in its own words — `--no-mcp`
  disables "conventional MCP autoload (native user ~/.gjc/agent/mcp.json and
  project .gjc/mcp.json registrations)" — and `-r, --resume[=<value>]` takes an
  ID prefix, a path, or opens a picker.
- On 0.17.1 its screens could not be read: every entry point, `gjc mcp list`
  and `gjc -p` included, exited with `Cannot find module
  '../../../../node_modules/mupdf/dist/mupdf-wasm.wasm'`.

0.17.2 runs (it wants Bun 1.4 and says so), and the rows above are its own
screens rather than its documentation:

- `gjc customize doctor` reports `~/.gjc/agent/extensions/deja.ts` as "a
  trusted filesystem extension module discovered for session-start loading",
  and `~/.gjc/agent/commands/deja.md` as a loaded slash command.
- The same screen is where the skill location proves itself: a copy under
  `~/.agents/skills` is listed `source-ignored`, "GJC loads skills only from
  .gjc (native) locations", which is why `deja install gjc` writes gjc's own
  directory.
- `gjc -p` against a mock provider carried deja's `<deja-recall>` block into
  the request — the past session, the question it answered and the conclusion —
  on both the OpenAI (`/v1/responses`) and Anthropic (`/v1/messages`) paths,
  and the server in `mcp.json` was connected in the same run: the tool
  inventory in the system prompt lists `deja/deja: mcp__deja_deja`.
