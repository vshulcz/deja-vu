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

**Last verified:** 2026-09-16

## Known quirks and drift

- **Sub-agent passes sit one directory deeper**, under a directory named for the
  session they belong to, one file per pass. They are skipped: a sub-agent's
  transcript repeats the parent's work in its own words, and indexed as a
  session of its own it competes with the parent for the same recall slot.
  `DEJA_INCLUDE_SUBAGENTS=1` takes them, the switch Claude Code's and Cursor's
  sub-agents already use.
- `service_tier_change` lines are not turns and are dropped rather than read as
  empty messages.
- Wiring: `deja install gjc` writes the server into `~/.gjc/agent/mcp.json`
  and the skill into `~/.gjc/agent/skills/deja-search/SKILL.md`. Both paths
  are from gjc's own surface table (`docs/customization.md`), and the skill
  location matters: gjc loads its native skills directory, while Claude's and
  Codex's are import candidates it does not read, so a skill written there
  would be a file no session ever sees.
- Auto-recall is the next step rather than a config line. gjc's native hooks
  carry pi's event names — `session_start`, `before_agent_start`, `tool_call`
  — but they are TypeScript modules loaded with Bun `import()`, so this is
  pi's extension ported. Its two documents also disagree on the directory
  (`~/.gjc/hooks/{pre,post}` against `~/.gjc/agent/hooks/{pre,post}`), which
  has to be settled against the loader before anything is written there.
