# Kimchi Coding

- **ID**: `kimchi`
- **Store**: `${KIMCHI_CODING_AGENT_DIR:-${XDG_CONFIG_HOME:-~/.config}/kimchi/harness}/sessions/<session>.jsonl`
- **Read override**: `DEJA_KIMCHI_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

Kimchi Coding is another pi descendant and writes the same envelope, so the
parsing is pi's.

**Last verified:** 2026-09-16

## Known quirks and drift

- Resume: `kimchi --session <id>`. Its own argument parser rewrites
  `--resume <selector>` to `--session <id>` (`src/cli-args.ts`), so the id
  deja indexes is the selector Kimchi takes — `deja resume` prints that
  command rather than handing over a paste.
- The root is flat — one directory, every session in it — so there is no encoded
  project directory to read a name from. The header line's `cwd` is what names
  the project, the same choice omp and prime-agent make for the same reason;
  drop it and every Kimchi session lands under no project at all.
- The default root sits under the config home rather than a dot directory of its
  own, so `XDG_CONFIG_HOME` moves it. `KIMCHI_CODING_AGENT_DIR` moves it
  outright.
- Wiring: `deja install kimchi` writes the server into `<agent dir>/mcp.json`
  — `join(getAgentDir(), "mcp.json")` in Kimchi's own
  `src/extensions/mcp-adapter/config.ts`, so `KIMCHI_CODING_AGENT_DIR` moves
  it for the installer the same way it moves it for the reader.
- Everything past the tool is behind one of Kimchi's own switches, and both
  ship disabled: `kimchi resources enable extensions.claude-code-hook-adapter`
  runs the hooks `deja install claude` already wrote, and
  `extensions.claude-code-skills` loads the skill from `~/.claude/skills`.
  deja records those as blocked rather than claiming auto-recall it does not
  control.
