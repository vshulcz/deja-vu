# Cherry Studio

- **ID**: `cherrystudio`
- **Store**: `<app data>/CherryStudio/Data/Agents/.claude/<projects>/<workspace>/<session>.jsonl` — `~/Library/Application Support/CherryStudio` on macOS, `%APPDATA%\CherryStudio` on Windows, `$XDG_CONFIG_HOME/CherryStudio` elsewhere
- **Store (before the upgrade that added `Data/Agents`)**: `<app data>/CherryStudio/.claude/<projects>/…`, read so a store that predates it is not lost
- **Read override**: `DEJA_CHERRYSTUDIO_ROOTS` replaces the root list
- **Format**: Claude Code's own transcript JSONL
- **Needs**: nothing

Cherry Studio runs Claude Code sessions from a desktop app and writes ordinary
Claude Code transcripts under its own app data, so the parsing is Claude's.

**Last verified:** 2026-09-16

## Known quirks and drift

- **A snapshot per stream chunk.** Cherry Studio appends the same API call three
  or four times as the response streams: a new `uuid` each time, the same
  `requestId` and `message.id`, and the text growing. Read plainly that is one
  reply stored three times in prefixes — a recall can then quote half a sentence
  and `deja show` prints the answer twice before finishing it. The reader
  collapses a run by `requestId` (else `message.id`, else `uuid`) and keeps the
  longest text. Stock Claude Code writes one record per turn, so its reader is
  unchanged and a test pins that.
- Sessions are their own harness rather than extra Claude roots: a reader should
  see which app the work happened in, and `deja sources` says `cherrystudio`.
- Read support only. Nothing is wired into Cherry Studio — no hooks, no MCP, no
  `deja install` — and since it embeds Claude Code rather than being it, that
  question belongs to the app's own plugin surface.
