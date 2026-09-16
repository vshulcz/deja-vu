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
- Read support only.
