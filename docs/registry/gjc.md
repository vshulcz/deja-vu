# gajae-code

- **ID**: `gjc`
- **Store**: `${GJC_CODING_AGENT_DIR:-~/.gjc/agent}/sessions/<project>/<session>.jsonl`
- **Sub-agent passes**: `…/<project>/<session>/N-*.jsonl`
- **Read override**: `DEJA_GJC_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

gajae-code (`gjc`) is a pi descendant: a `session` header carrying the id and
the cwd, then one `message` line per turn, so the parsing is pi's. The encoded
project directory names the project, and the header's cwd wins when it is there.

**Last verified:** 2026-09-21

## Known quirks and drift

- Resume: `gjc --resume <id>`, in the directory the header's `cwd` names.
  From any other project 0.18 refuses the session ("is in another project …
  Re-run from that directory") or forks it into the current one, so deja
  prints the `cd` (#4395).
- **Sub-agent passes sit one directory deeper**, under a directory named for the
  session they belong to, one file per pass. They are skipped: a sub-agent's
  transcript repeats the parent's work in its own words, and indexed as a
  session of its own it competes with the parent for the same recall slot.
  `DEJA_INCLUDE_SUBAGENTS=1` takes them, the switch Claude Code's and Cursor's
  sub-agents already use.
- **Project directories**: from 0.18, `<project>` is `v2-<identity digest>`
  rather than a slug of the path, with `.gjc-managed-session-scope.v2.json`
  beside the sessions naming the directory. The header's cwd gives the
  project either way. `deja doctor` leaves the scope file and the skipped
  passes out of its "not recognised here" count (#4393).
- **Edits are hashline, not pi's.** gjc 0.18's `edit` rejects pi's
  `{path, edits}` ("input: expected string") and takes one `input` string:
  `§path`, then ops (`≔A..B` replace, `«A` insert before, `»A` insert after),
  each followed by the lines it writes. deja records the file and the written
  lines from the call, and the replaced lines from the result's
  `details.diff` (`-1|old line`), since the call holds only anchors (#4113).
  `edit.mode` picks the shape: hashline is one of four. `replace`, the auto
  choice for Claude, DeepSeek and Qwen models, takes `{path,
  edits:[{old_text, new_text, all}]}`, `patch` takes `{path, edits:[{op,
  diff}]}` and `apply_patch` an `input` patch; all are read for the file, the
  replaced span and the written lines (#4524).
- `service_tier_change` lines are not turns and are dropped rather than read as
  empty messages.
- Wiring: `deja install gjc` writes the server into `~/.gjc/agent/mcp.json`,
  the skill into `~/.gjc/agent/skills/deja-history/SKILL.md` and the `/deja`
  command into `~/.gjc/agent/commands/deja.md`. The first two paths
  are from gjc's own surface table (`docs/customization.md`), and the skill
  location matters: gjc loads its native skills directory, while Claude's and
  Codex's are import candidates it does not read, so a skill written there
  would be a file no session ever sees.
- Auto-recall is `deja install gjc-auto`, which adds pi's extension unchanged
  at `~/.gjc/agent/extensions/deja.ts`:
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
