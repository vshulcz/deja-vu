# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.15.0] - 2026-07-21

### Added
- Déjà vu moments: when a prompt matches work your own history already answered, the per-prompt recall now announces it with a visible one-liner — `deja-vu: you have been here — "<that session>" (3w ago)` — and the moment is counted in stats and the weekly numbers.
- Bare `deja` on a terminal shows a living brief instead of help text: today's sessions, recalls served and what they distilled, this week's déjà vu moments, recent sessions, and a suggested search from your own history. `deja help` keeps the usage text.
- The session-start recall receipt now carries the day's tally: how many recalls deja served today and how much history they distilled.

### Fixed
- `deja uninstall` could leave a hook entry with `"hooks": null` in Claude Code's settings.json when the entry carried a matcher — Claude Code then rejected the whole settings file. Uninstall now drops the entry, and any damaged entry from an earlier version heals on the next install.

## [0.14.4] - 2026-07-21

### Added
- The first index greeting now suggests a search phrase taken from your own recent history instead of a generic hint.
- `deja stats` and the statusline show what recall replaced: served bytes against the source transcripts they were distilled from, as a personal ratio measured from real events.
- Search learns from use: sessions that agents keep recalling rank slightly higher (hard-capped at +20%), marked `reused N×` in output and as an additive `reused` JSON field.
- Co-occurrence rescue: on zero results, one query token may swap for a neighbor the corpus itself ties it to — a 245 KB map built at full rebuild, narrated like any variant.
- Compound identifiers answer for their parts: `deja "user profile"` finds `getUserProfile` and `refresh_token_rotation`.
- A reviewed developer-synonym table (k8s↔kubernetes and friends) and Russian suffix folding join the stem tier.

### Changed
- Ranking now weighs term proximity and title matches (both bounded) on top of BM25 and freshness.
- The postings AND considers up to 8 query tokens instead of the 3 longest, so a rare token narrows candidates before the scan.
- `deja blame` human output is colored like search results.

## [0.14.3] - 2026-07-21

### Added
- Kimi Code is the twelfth harness: `wire.jsonl` transcripts are indexed retroactively (streamed assistant turns reconstructed from loop events, mid-stream responses survive incremental indexing), `deja install kimi` wires MCP through `$KIMI_CODE_HOME/mcp.json` plus an `AGENTS.md` guidance file, and `deja resume` reopens sessions via `kimi --session`. Spec contributed by @yearth (#248).

## [0.14.2] - 2026-07-21

### Added
- `deja log` — audit trail of served memory: recent recalls and injections as a table, `--last` prints the exact digest most recently injected (hook, per-prompt, MCP, handoff), from a size-capped snapshot file next to the usage sidecar.
- Entropy redaction: bare high-entropy values in secret-shaped positions (any assignment's value side, including the Telegram `digits:token` shape, or a token alone on its own line) are now stripped at index time. Hex digests, UUIDs, paths and identifiers are excluded; measured no rebuild-time cost.
- Earlier-attempt flags: when an old session and a newer one from the same project match the same ground, the older hit is labeled with the newer session's date in CLI output and MCP digests, plus an additive `superseded` field in `--json`.

### Fixed
- Natural-language queries no longer die on the AND: suffix stemming works in both directions (`failing` finds `fails`), short tokens get plural forms, and up to two tokens that no session can satisfy are dropped with explicit narration instead of returning zero results.

### Changed
- MCP recall digests show a relative age next to each session date.
- `stats --card` renders in the site's terminal look: rewind-loop mark and wordmark in the header, scanline texture, accent punchline.

## [0.14.1] - 2026-07-21

### Changed
- Internal restructuring: index engine split into ingest/retrieval/manifest/store-IO files, stats and share/handoff digest building moved to internal packages, table-driven subcommand dispatch, index directory resolved once at startup. No user-facing behavior change.

## [0.14.0] - 2026-07-21

### Added
- Handoff/resume commands corrected against the real installed CLIs: cursor handoff uses positional prompt (no `chat` subcommand exists), pi resume uses `--session`, and grok is marked non-resumable (it has no session flags).
- Per-prompt recall (UserPromptSubmit) now ranks THIS project's sessions by IDF-weighted overlap with the prompt instead of reconstructing an AND query — natural prompts are full of filler that poisoned the old query builder into empty or wrong hits. Excludes the current/too-fresh sessions, dedupes per agent session, and appends a ready citation line.
- `deja stats` counts how often agents actually said "deja-vu recalled" — a telemetry-free measure of memory credited aloud, closed by deja re-indexing those transcripts.
- Ingestion health: malformed JSONL lines and failed file parses are counted per harness, persisted in the manifest, and surfaced in `deja doctor` (details in `--json`). Tolerated loss now leaves evidence instead of disappearing.
- Harness capability matrix (MCP / auto-recall / resume / handoff / prerequisites) generated from the format registry into README and the site; a conformance test pins the published matrix to actual code behavior.
- `deja resume` reopens Copilot CLI sessions (`copilot --resume=<id>`).
- Copilot CLI is now a full MCP target: `deja install` writes `~/.copilot/mcp-config.json` (verified live — Copilot calls deja's recall over MCP), replacing the guidance-only stub.

### Changed
- Handoff digests now end with a pull pointer (source session id + how to recall deeper), turning a lossy one-shot push into push+pull; `deja stats` counts sessions started from a handoff.
- The weekly recall headline counts only agent-initiated, non-empty recalls; auto-injections are reported separately. The recall receipt fires only when the recalled set changed, not on every session start.

### Fixed
- SSH sync push is acknowledged delivery: export watermarks advance only after the remote import succeeds, so a failed transfer no longer silently drops that batch from every later push. Pull failures after the remote export now print the exact recovery command.
- One malformed session file no longer aborts a full rebuild or an incremental pass: parsers are panic-guarded per file and per harness.
- Crash-hardening for in-place index writes (#181): bucket files are replaced atomically, the record log is fsynced before the manifest stamps its size, an uncommitted record tail now triggers a rebuild instead of silently duplicating messages, and full rebuilds keep the previous index recoverable through the rename window.
- MCP recall/blame accept a fractional `limit`: a client that serializes `5` as `5.0` used to get a `-32602` error and no results at all.
- MCP server hardening: large JSON-RPC ids are echoed back exactly instead of being rounded through float64, and a single oversized frame is skipped rather than tearing down the whole stdio session.
- `manifest.gob`/`sessions.gob` are fsynced before the rename, closing a crash window that could leave a torn manifest even though the rest of the index writes durably.
- A session file caught mid-write (torn first line) is fully re-indexed on the next pass instead of resuming an append mid-line, so its first message is no longer dropped.
- `deja install` writes the Windows `cmd /c` shim for Codex and Grok `config.toml`, matching the JSON-based installers.

### Added
- Recall receipt: when auto-recall injects real context, the SessionStart hook now surfaces a one-line notice ("deja: recalled N prior sessions…") instead of working silently; `deja stats` and the statusline report the trailing-week recall count and re-used context volume.
- GitHub Copilot CLI as the eleventh harness: sessions in `~/.copilot/session-state` are discovered, parsed and incrementally indexed; `copilot` is also a handoff target.
- `deja handoff --to <agent> [id-prefix] [--exec]` — package the live context of a session (problem, conclusions, where it stopped) and continue it in a different agent. Composable: `codex "$(deja handoff --to codex)"`; `--exec` launches the target directly. Targets: claude, codex, opencode, gemini, qwen, aider, pi, grok.
- Shareable stats card rebuilt around a trailing-year activity grid with a personal headline; `stats --card` now runs quietly and prints a paste-ready snippet.

## [0.13.1] - 2026-07-19

### Added
- Semantic search fallback: on zero lexical results with a current embedding sidecar, the query is vector-searched against it; results carry a semantic flag.
- Confidence tiers on every hit — exact, close (with the matched variant), semantic (with the cosine) — across CLI, JSON and MCP output.
- Natural-language queries: query-time stop-word dropping and a morphological fallback, so the README's own example phrasing finds its session.
- PreCompact capture and post-compaction re-injection: the transcript is indexed before Claude Code compacts, and the SessionStart digest re-anchors the model afterwards with visible per-session provenance.
- Onboarding builds memory on install: install --auto/--all index detected stores on the spot, the SessionStart hook warms a missing index in the background, and install.sh offers a PATH line.
- Personal headline metrics in stats and an embeddable SVG card (counts only, no project names) with a ready-to-paste markdown snippet.
- MCP tool descriptions rewritten around user trigger phrases, with read-only annotations; the injected digest opens with an actionable line.
- deja bench context: a seeded, ablation-armed context-readiness experiment with coverage gates and negative controls.
- A Dockerfile for directory checkers, and automatic publication of server.json to the MCP registry on release.
- pi (pi.dev) as the tenth supported harness (contributed by @maxandersen).

### Fixed
- Incremental update no longer drops untouched Cursor sessions from the index.
- The search/MCP build path dedups messages, so a session present in two stores is not double-indexed.
- `deja forget` writes tombstones before rebuilding so a crash cannot resurrect forgotten sessions; `unforget` matches by id-prefix so a bare letter cannot revive whole harnesses.
- The aider parser reads unbounded lines, so a multi-megabyte pasted blob no longer drops later sessions.
- Fuzzy and word-form hits rank by relevance (BM25) instead of recency only; stop words no longer over-constrain natural-language queries.
- Install writes the MCP command through `cmd /c` on Windows so stdio clients can spawn it.
- `recall_context` no longer returns a header-only digest for multi-word queries (community contribution).
- A corrupt record length prefix is rejected instead of allocating gigabytes.

### Security
- Redaction now covers HTTP Basic auth, `scheme://:password@host` URLs and PGP armored keys, runs before the size cap so a boundary-straddling secret is not stored raw, and the `sk-`/`xai-` rules no longer destroy kebab-case prose (the xai- fix is a community contribution).

## [0.13.0] - 2026-07-19

### Added
- BM25 ranking with a user-message boost, quoted phrase queries, and a typo fallback that only runs on zero results.
- Optional semantic recall: `deja embed` builds a vector sidecar from a local Ollama/LM Studio endpoint; search and MCP recall blend it with the lexical score.
- `deja blame <path>` and an MCP `blame` tool: sessions that discussed a file, newest and most specific first.
- `deja remember` and an MCP `remember` tool: durable notes stored as a tenth source with full redaction, sync and provenance.
- Privacy set: `deja forget` with persistent tombstones, ingest exclusion patterns, and `deja stats --redaction` per-rule reports.
- `deja stats --card` (shareable SVG) and `deja stats --html` (self-contained, metadata-only timeline).
- Qwen Code as the ninth harness, `deja last --project/--harness` filters, a session format registry with conformance fixtures, and a reproducible `deja bench recall`.
- User-level agent guidance written by install for Claude Code, Codex, Gemini, opencode, Antigravity, Qwen and Copilot.

### Fixed
- Torn lines longer than one scan window no longer lose messages.
- `GROK_HOME`/`DEJA_GROK_ROOT` split: session-read overrides no longer move where install writes config.

### Security
- Index and usage files are created owner-only; install backups and new agent configs are 0600.
- `deja resume` refuses session ids with shell-unsafe characters.
- Agent-facing recall output is framed as untrusted historical data.

## [0.12.0] - 2026-07-17

See the release notes: harness coverage through Grok Build, `deja update`, signed checksums, npm and install-script distribution.

## [0.11.0] - 2026-07-16

See the release notes.

## [0.10.0] - 2026-07-16

See the release notes: Antigravity harness, share redaction hardening.

## [0.9.2] - 2026-07-16

### Added
- MCP install targets for Cursor (~/.cursor/mcp.json), Gemini CLI (~/.gemini/settings.json) and Antigravity (~/.gemini/config/mcp_config.json); install --all picks them up.

### Fixed
- Cursor searches no longer re-merge the whole index on every call: the state store carries a watermark and incremental passes fetch only new messages (#72).
- The same chat arriving from two stores (gemini .json/.jsonl, cursor multi-store) no longer duplicates messages.
- Sync import keeps messages that share a timestamp within one session.
- deja sources lists all seven harnesses and attributes opencode redaction counts correctly.
- deja resume covers all seven harnesses — native commands where they exist, honest guidance where they do not.
- deja stats aligns and colors every harness tag.

## [0.9.1] - 2026-07-16

### Added
- Antigravity support: transcripts are read from the plaintext per-conversation logs, so history stays searchable even where its conversation db is encrypted. Seven harnesses now feed one index.

## [0.9.0] - 2026-07-16

### Added
- Three new harnesses: Cursor (IDE chats from state.vscdb plus CLI agent transcripts), Gemini CLI (both storage generations, including $rewindTo replay) and aider (markdown chat history with fence-aware parsing). deja now indexes six coding agents into one memory.
- `DEJA_AUTORECALL_LOCAL_ONLY=1` keeps synced sessions out of session-start auto-recall.

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
