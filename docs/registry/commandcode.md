# Command Code

- **ID**: `commandcode`
- **Store**: `~/.commandcode/projects/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_COMMANDCODE_ROOT` replaces the project root
- **Format**: flat JSONL — one message per line
- **Needs**: nothing

Command Code writes a Claude-Code-style project directory with a plain
transcript in it: each line is one message, carrying `role`, `content`,
`timestamp` and `sessionId`, with no envelope around it. `content` is either a
string or the block array Claude's format uses, and both are read.

**Last verified:** 2026-09-16

## Known quirks and drift

- **A sibling with the same extension.** `<session>.checkpoints.jsonl` sits
  beside the transcript and is a snapshot stream, not a conversation — read as
  one it adds a session with no words in it that then competes for a recall
  slot. It is skipped by name, and a test pins that.
- A line whose role is neither `user` nor `assistant` is tool output, which is
  where a command's error text lives. It is indexed as tool output rather than
  dropped, so a user can search for an error they have already hit.
- Wiring: `deja install commandcode` writes all four of its user-scoped
  surfaces, and `commandcode-auto` adds the hooks —
  `~/.commandcode/mcp.json` for the server, `skills/deja-search/SKILL.md`,
  `commands/deja.md` for `/deja`, and a `hooks` key in `settings.json` in
  Claude's shape.
- **Two things there fail quietly if copied from another harness.** The
  timeout unit is seconds, not milliseconds — the numbers deja passes to
  other settings.json targets would be ten minutes each, past the documented
  600-second maximum. And the tool matcher is a regex over Command Code's own
  display names — `SHELL`, `READ`, `EDIT`, `WRITE`, `SEARCH`, `GLOB`, `LIST`
  — so a Claude-shaped `Bash` matcher never fires at all. Both are pinned by
  tests.
- There is no per-prompt event: the four documented are `SessionStart`,
  `PreToolUse`, `PostToolUse` and `Stop`, so the digest rides SessionStart and
  the rest is the tool-time pair.
- **None of this is verified on the machine deja was written on.** The CLI is
  closed, and every path above comes from two independent integrations that
  both cite the vendor's docs: rulesync's `commandcode-paths.ts` and
  `types/hooks.ts`, and akitaonrails/ai-memory's support matrix, which reports
  the same MCP file and says its SessionStart injects. If a live install
  disagrees, this entry is where to correct it.
