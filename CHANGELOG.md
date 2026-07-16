# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.8.0] - 2026-07-16

### Added
- deja resume <id-prefix> reopens a found session in its native harness (claude --resume, codex resume, opencode -s), recovering the original working directory where possible. --exec runs it directly.

### Changed
- Subagent transcripts are skipped by default; DEJA_INCLUDE_SUBAGENTS=1 opts back in.

### Fixed
- A session file caught mid-write no longer loses its torn tail line: appends resume from the last complete line and pick the message up exactly once.

## [0.7.0] - 2026-07-16

### Added
- `deja statusline` — one line for your status bar: recalls served to agents today and how much context that was. `deja install statusline` wires it into Claude Code without touching an existing statusline.
- Session-start auto-recall for Codex (hooks.json) and opencode (generated plugin). `deja install --auto` now covers every harness it finds.
- `deja hook-context --plain` prints the bare digest for hosts that inject raw text.

## [0.6.0] - 2026-07-14

### Added
- `deja sync ssh <host>` — one-command sync between machines over system ssh/scp, `--pull` for the reverse direction.
- `deja sync export --full` re-exports everything regardless of watermarks, for onboarding a new machine.
- `deja warmup` builds the index without searching.
- `deja sources` warns when the sqlite3 CLI is missing instead of silently showing zero opencode sessions.

### Fixed
- Sessions replaced during a non-append index update kept stale posting ordinals and dropped out of search.
- A bucket file corrupted by a crash now triggers one automatic rebuild instead of erroring until a manual `--rebuild`.
- Claude project names resolve against the filesystem, so `deja-vu` no longer displays as `deja/vu`.

## [0.5.2] - 2026-07-14

### Fixed
- Sync-imported records survive full rebuilds and incremental index updates; re-import stays idempotent.
- Redaction bookkeeping no longer creates phantom source-file entries that purged imported records.
- Exports skip imported records, so bidirectional sync does not echo history back to its origin.
- The first record of a sync import got a wrong posting offset and was unsearchable.

## [0.5.1] - 2026-07-14

### Changed
- `deja share` filters pasted JSON, diff and CLI dumps out of digests.
- Session titles and stats skip tool-wrapper and caveat noise.

## [0.5.0] - 2026-07-14

### Added

- `deja share <id-prefix>` sanitized markdown session digests for handing context to colleagues.
- `deja sync export <dir>` and `deja sync import <dir>` append-only JSONL batches with export watermarks and idempotent imported-session ingest.

## [0.4.0] - 2026-07-14

### Added

- `deja stats` shareable indexed-work summary with totals, harness breakdown, top projects, 12-month activity sparkline, date range, longest session, busiest day, and `--json` output.

## [0.3.0] - 2026-07-14

### Added

- Optional Claude Code auto-recall via `deja install --auto`, which installs the MCP server and a read-only `SessionStart` hook that injects a capped project-session digest from the warm local index.

## [0.2.0] - 2026-07-14

### Added

- Secret redaction at ingest before records are written to the local index, with manifest counters and `deja sources` redaction totals.

## [0.1.1] - 2026-07-14

### Fixed

- Session ranking lost results for sessions present in two stores.
- The sqlite3 CLI could create a stray opencode.db side-effect file.
- Substring queries (code finds opencode) work through the index again.
- Switching --harness no longer rebuilds the whole index.
- Multi-word snippets anchor and highlight correctly.

### Changed

- Binary index format; warm search 7-9 ms typical.
- Releases publish to GitHub, Homebrew and npm from one tag.

## [0.1.0] - 2026-07-14

### Added

- Local search across Claude Code, Codex CLI, and opencode histories.
- Incremental on-disk index for fast repeated search.
- `deja ctx` compact context output for the best matching session.
- Stdio MCP memory server with `recall` and `recall_context` tools.
- Idempotent installers for claude-code, codex, and opencode MCP config.

[Unreleased]: https://github.com/vshulcz/deja-vu/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/vshulcz/deja-vu/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/vshulcz/deja-vu/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/vshulcz/deja-vu/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/vshulcz/deja-vu/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/vshulcz/deja-vu/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vshulcz/deja-vu/releases/tag/v0.1.0
