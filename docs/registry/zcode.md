# ZCode

- **ID**: `zcode`
- **Store**: `~/.zcode/projects/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_ZCODE_ROOT` replaces the project root
- **Format**: flat JSONL — one message per line
- **Needs**: nothing

ZCode is Z.ai's desktop agent and writes the same flat transcript Command Code
does, under the same project layout: `role`, `content`, `timestamp`,
`sessionId`, one message per line. Some lines also carry the API's `usage`
block, which deja has no use for and ignores.

**Last verified:** 2026-09-16

## Known quirks and drift

- **A SQLite store beside the transcripts, unread.** No sample of that schema is
  in hand, and a reader written against a guessed shape is one that skips the
  half of a store it does not understand without saying so. The transcripts are
  the conversation either way; the database becomes work the day a sample of it
  exists.
- Wiring: `deja install zcode` writes the server into `mcp.servers` in
  `~/.zcode/cli/config.json` — one level deeper than the `mcpServers` every
  other client here uses — and `deja install zcode-auto` adds the hooks to the
  same file, on `SessionStart` and `UserPromptSubmit`.
- **Three things decide whether that works, and all three are silent when
  wrong.** Config-file hooks do nothing without `hooks.enabled: true`. A
  config hook gets no template expansion, so the command carries an absolute
  path. And the output schema is strict: one key ZCode does not recognise and
  the whole response is discarded — which is why the installed line ends in
  `--strict`, dropping deja's receipt line and keeping the context.
- The shapes were not read from ZCode's own documentation, which does not
  describe them. They come from volcengine/OpenViking's memory plugin, whose
  `examples/agent-hook-plugin/DESIGN.md` records the surface it established by
  inspecting a live install — seven hook events, the manifest probe order, the
  strict schema — and ships an installer against it. Nothing here is verified
  on the machine deja was written on.
