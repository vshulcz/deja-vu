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
- Read support only.
