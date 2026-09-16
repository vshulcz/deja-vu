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

The fixture in this repository sits at `fixtures/registry/cherrystudio/projects/…` rather than
under a `.claude` directory: the repository excludes `.claude/`, so a fixture carrying that
segment is never committed and the tests pass only on the machine that wrote it. The parse does
not depend on the directory shape — the roots come from the environment — and the real layout is
the one above.

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
- Wiring: `deja install cherrystudio` writes
  `<config>/deja/cherrystudio-mcp.json` and says to import it in Settings → MCP
  → Import from JSON. The app keeps its MCP servers in its own SQLite store — a
  drizzle schema seeded from a built-in preset list
  (`src/main/data/services/McpServerService.ts`,
  `src/main/data/db/seeding/seeders/builtinMcpServerSeeder.ts`) — so there is no
  config file to write, and writing into a running app's database is not an
  installer's job. Its import paths are JSON, DXT and MCPB
  (`src/renderer/pages/settings/McpSettings/McpServersList.tsx`); the MCPB
  bundle this project already publishes is the other one and needs nothing new.
- No hook surface for a third party, so auto-recall is recorded as impossible
  rather than as a gap someone could close: the agent sessions run inside the
  Electron app, and the extension points are the MCP server list and its import
  paths. Same for slash commands. A skill channel was not found either — the
  guidance reaches the model through the MCP tool descriptions.
