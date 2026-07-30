# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `deja handoff --to agy` starts Antigravity with the digest as its first prompt. It was listed paste-only on the reading that it is a GUI, but its terminal client is `agy` — the binary `deja resume` already calls — and `agy -i <prompt>` opens an interactive session on it. `--to antigravity` is the same target. Thanks to @shgpavel. (#524)

### Fixed
- `deja handoff --to hermes|openclaw|roo` answered `don't know how to hand off` instead of printing the digest to paste; the paste path now covers every harness the registry marks paste, kept in sync by the capability drift test. Thanks to @shgpavel. (#524)

## [0.16.3] - 2026-07-30

### Fixed
- Relevance results reported the window instead of the pool they came from: `total: 50, capped: false` however deep the candidate set went. `total` is now the pre-truncation count and `capped` says whether anything was withheld, on every relevance exit including the merged close-tier tail. Found, diagnosed and fixed by @AliceLJY. (#497, #554)
- Harness plumbing was indexed as if a person had said it — `[Request interrupted by user]`, system reminders and slash-command echoes were searchable and counted in stats. 304 records on a real index. Only complete blocks are stripped, so a message that merely mentions a marker keeps its text. (#551, #555)
- The winget version manifest was left at the previous release by `pinmanifests`, so the manifest set failed its own consistency test after every release. (#523)

### Changed
- The comparison page covers the four largest memory projects beyond the six it started with, and no longer claims retroactive indexing is unique to deja — MemPalace mines Claude Code, Codex and Cursor transcripts too. (#310, #525)

### Fixed
- `deja handoff --to hermes|openclaw|roo` answered `don't know how to hand off` instead of printing the digest to paste; the paste path now covers every harness the registry marks paste, kept in sync by the capability drift test. (#524)

## [0.16.2] - 2026-07-29

Ranking learned to tell a conclusion from a conversation about one, and search
stopped reading a session it was never going to rank on.

### Added
- Search output says which tier answered (`exact`, `close`, `stemmed`, `semantic`, `relevance`), how many sessions matched, and whether the cap hid any. Both the text and JSON forms carry it. Thanks to @AliceLJY for the naming. (#494, #495, #496)
- A decision that was later reversed comes back marked — `[this was tried and rejected, 2026-07-29]` with the reason — instead of being served as current truth. (#506)
- `.mcpb` bundles for the registries that install MCP servers from one, with the entry point fixed to a real server rather than a stub. (#484, #487)
- `deja --help` lists the search flags. Thanks to @AliceLJY. (#493)

### Changed
- A session that concluded something now outranks one that only discussed it, and a pasted log ranks below a human answer to the same question. Measured on a benchmark built for it: 8/8 and 8/8, LongMemEval-S unchanged. (#509, #510)
- Reuse counts a déjà vu moment — the user returning to the same ground — alongside agent recalls, and the 1.2× ceiling on it is now covered by a test rather than a judgement. (#511)
- Coverage is measured on shipped packages; benchmark harnesses under `scripts/` no longer count, and the floor rose from 82.0% to 87.5%. (#515)

### Performance
- A query over a common word reads at most 64 matching messages of any one session, sampled across it. `"index"` on a 3.5 GB store: 61.8 ms → 26.9 ms. Nothing under the bound is touched, and no session is ever dropped from the candidates. (#513, #515)
- Records are read in coalesced spans instead of three syscalls apiece. (#504, #512)
- Full rebuild is 12% faster: redaction scans bytes instead of running a regex, and the opencode query stops parsing rows that cannot match. (#498)
- Claude transcripts decode into declared types — a third fewer allocations. (#502)

### Fixed
- The first index built by running a search — which is how nearly everyone builds their first one — reported no progress at all: a spinner reading "starting" and a bar frozen at one notch for the whole build. (#505, #517)
- deja indexed its own recall blocks, so a session could be answered with deja's own earlier answer. (#480, #488)
- Recall returned the question rather than the decision that followed it. (#490, #491)
- `deja-stats.svg`, a generated card from a local run, had been committed to the repo. (#514)
- The front page quoted a search latency measured before two changes to the read path. It is 1.3 ms median and 17 ms on the most common word, both from `scripts/searchbench`; the LongMemEval median search was quoted at 40 ms and is 19 ms. (#519)
- The file-ranking test gave two git calls 400 ms and failed on runners that miss it. (#516, #520)

## [0.16.1] - 2026-07-28

### Added
- MCP bundles: every release now ships an `.mcpb` per platform, so desktop apps that install MCP servers as bundles can install deja by opening one file. The bundle carries the binary — no runtime, no package manager, nothing hosted. Terminal agents keep using `deja install`. (#481, #482)
- A documented machine-readable read contract: `deja last --json`, `deja show --json --harness <name>` with `--offset`/`--limit`, an explicit `--limit` on search, and a `source` field recording whether a session is local or imported. Exact reads require both id and harness, because ids collide across harnesses and a machine reader cannot notice. Thanks to @adamsitar. (#476)

### Fixed
- Windows scoop and winget manifests were pinned to 0.15.6, so those two channels were two releases behind. (#478)
- Renamed the SQL escaping helper to say what it does; the old name had caused two silent query failures in a day. (#475)


## [0.16.0] - 2026-07-28

Every harness that can inject context now recalls on its own, and `deja doctor`
tells you which of your integrations are actually live.

### Added
- Auto-recall in every harness that can inject context, rather than only the ones with a documented hook. (#362)
- Installable from the Claude Code and Codex marketplaces; the same bundle installs into six registries. (#367, #391)
- Hermes Agent sessions are indexed, with a plugin, MCP wiring and support for its flat store layout. (#376, #389)
- pi gets `/deja` and keeps recall stats in its footer; opencode toasts the recall receipt and shows the first build as a moving bar. (#381, #384, #392)
- aider, cline, goose and roo install to real recall: aider re-reads a `read:` context file every message, cline gets a plugin whose rule is built at session start, goose injects through MOIM (which survives compaction) plus an MCP extension, and roo installs into every host it has run in, following `roo-cline.customStoragePath` when set. (#396, #400, #405, #410, #426)
- The plugin bundle ships the MCP server, so the six registries that install Claude-format plugins — Claude Code, Codex, Cursor, Qwen, OpenClaw, Copilot CLI — get recall tools without a second install step. (#394)
- `deja doctor` reports every auto-recall integration as wired, stale or missing, instead of only checking the one you asked about. (#417)
- `deja handoff` starts cline, goose and kimi directly, and hermes sessions resume by id. (#450, #452)
- `deja bench prompt` scores recall per prompt against a per-topic corpus, with negative controls and gated marathon/fresh shapes. (#424)
- Time queries understand month names in English and Russian, resolving to the most recent occurrence. (#456)
- Nightly snapshot builds publish a rolling prerelease from main. (#398)

### Fixed
- Harness wiring that was installed but not working: gemini hooks live in an extension rather than `settings.json` and SessionStart was never hooked, qwen's hook needed a millisecond timeout, kimi hooked the wrong event and could not read prompts sent as parts, codex left an existing entry stale instead of adopting it, and installing twice added a second hook rather than updating the first. (#373, #378, #382, #385, #386, #388)
- opencode recalled only at session start, and lost the transcript to compaction before it was indexed. (#383, #390)
- Auto-recall never fired for CJK prompts, Traditional and Cantonese function words were weighed as content, and Traditional/Simplified recall only worked in one direction. (#368, #369, #370)
- Read paths waited out a rebuild instead of serving the index they already had. (#380)
- `deja uninstall --all` left every hook and plugin in place, pointing at a binary it had just removed. (#421)
- Upgrading deja never repaired wiring an older version had written, so an integration could sit dead for weeks with nothing reporting it. Recorded targets are rewritten on the first session start after a version change. (#431)
- A rewound session kept its old text in the index: the append fast path compared size and mtime, which a rewrite can leave unchanged. It now checks a hash of the prefix it already read. (#454)
- Forgotten sessions came back if `~/.config/deja/tombstones` was lost. A second copy now lives beside the index. (#442)
- `deja goose` and `deja aider` launched an agent when given a one-word search. (#452)
- A harness that changes its database schema made its history vanish silently — all four SQLite parsers reported a failed query as an empty store, and grok's incremental filter had never worked at all. (#474)
- A read-only index refused to answer instead of serving what it already held. (#472)
- A query that happened to name a subcommand ran it. (#430)
- MCP clients that send numeric arguments as strings were rejected. (#423)
- doctor could not find the MCP server if it had been installed under another name, reported a codex hook as trusted when its hash no longer matched, and warned about sessions that were simply unused. (#428, #433, #440)
- Recall skipped long sessions instead of narrowing them, withheld the answer on old sessions rather than just the déjà vu line, and required more terms than most real questions carry. (#424, #438)
- `deja share` under-counted secrets by ignoring those redacted at index time; roo history was filed under cline; a bucket could read past the end of its posting block; an empty index printed nothing at all; `deja stats --card` appended a second `.svg`. (#432, #444, #448, #470, dc3fd84)

### Changed
- Every command spent about 650ms proving the index was fresh. Derived file state is carried forward when size and mtime match: 650ms -> 6ms. `deja doctor` no longer parses a 2.8GB database to answer a question about the newest session — 8s -> under 100ms. (#458, #460)
- Coverage is gated in CI per package, and the contributing bar is 90%. (#462, #463)
- Documented what `deja update` verifies and what it does not. (#446)


## [0.15.7] - 2026-07-25

Existing indexes are rebuilt once on first use: the on-disk format changed
several times in this release.

### Fixed
- Non-ASCII search was scanning the whole vocabulary on every lookup: tokens were sharded by their first two *bytes*, so a prefix plus half a UTF-8 sequence collapsed every Russian, Chinese and Greek token in the corpus into one bucket. Median search on a 100k-passage corpus: Chinese 4.02s -> 1.45s, Russian 1.50s -> 186ms. (#351)
- Russian inflection folding never fired — Cyrillic terms were handed to the ASCII stemmer, which appended English suffixes to them. Folding now covers the third-declension nouns (сеть/сети/сетью, новость/новостей) and short verb stems (знать/знал/знаю), while no longer reaching unrelated words: цель no longer recalls целая, часть no longer recalls час. (#351)
- Cross-machine sync: watermarks are per peer, a message sharing the newest timestamp is no longer skipped, tombstones and the exclude list survive a cache wipe, and a message the harness never stamped can now reach another machine at all. (#346)
- `--harness` and `--project` searches returned nothing when the unfiltered top of the ranking was full of other sessions — the scope was applied after truncation. Session ids could also collide during an incremental update, merging two sessions' postings. (#348)
- Recall kill switch and trust policy now bind every path, including the session-start hook cache. (#347)
- Chinese questions reach the relevance tier: fullwidth punctuation was glued into terms. Question grammar (在哪, 什么) no longer weighs as much as the entity asked about — MIRACL Chinese hit@1 40.4% -> 42.5%. (#345, #360)
- Relevance results no longer render "0 matches" with no snippet when a session surfaced through a folded form. (#352)

### Changed
- Index is smaller: records intern their session key and source path instead of repeating them (a real store wrote 90 distinct paths 57 000 times), large tool output and file dumps are deflated, and bucket directories no longer store an offset the reader can derive. A 1000-session store goes 66 MB -> 53 MB; the saving is larger the more tool output the corpus holds. Index build and search latency are unchanged. (#354, #357, #358)
- Search latency on queries that fall through the ladder: the token catalog is cached between queries instead of being rebuilt by both the stem and fuzzy tiers, and fuzzy matching only compares tokens whose length is within its edit limit rather than every token in the corpus — 168ms -> 36ms per term on a 267k-token vocabulary. (#350, #356, #360)

## [0.15.6] - 2026-07-24

### Added
- CJK bigram indexing (#337, design by @AliceLJY): Chinese/Japanese/Korean text gets first-class exact search and full ranking; index version bumps for one automatic rebuild. Note: a single-character query against a longer run (`茶` in `喝茶`) resolves via the close tier, not exact.
- Goose harness by @syf2211: legacy JSONL sessions and SQLite `sessions.db` (>= 1.10.0), resume via `goose session --resume --session-id` — sixteen agents indexed. ([#255](https://github.com/vshulcz/deja-vu/issues/255))
- Russian: conversational stop words and inflection folding in relevance ranking — сеть meets сетью and сети; instruction glue no longer anchors search or déjà vu.

## [0.15.5] - 2026-07-24

### Added
- `deja view` — browse your memory in one local HTML file: sessions with capped previews and client-side filtering, the verbatim recall audit trail, and curated notes with lifecycle badges. No server, no external assets; the page opens in your browser and nothing leaves the machine. (#334)

### Fixed
- Site: wide tables scroll inside their own container (mobile pages no longer overflow sideways), and the comparison page lost its broken navigation strip. (#333)

## [0.15.4] - 2026-07-24

### Fixed
- Agent startup never waits on digest work: the session-start cache is served at any age and refreshed by a detached process; the cache is also scoped per project directory, so switching projects no longer starts cold. Worst-case session-start on a dirty multi-gigabyte store: ~1s -> under 100ms; opencode startup overhead ~1.8s -> ~0.8s (the remainder is MCP process spawn). (#330)

## [0.15.3] - 2026-07-24

### Added
- Cline harness: both store generations (modern `~/.cline/data/sessions` and the VS Code globalStorage tasks), MCP wiring, resume for modern sessions. (#306)
- Roo Code harness: VS Code globalStorage tasks with per-task metadata. (#307)
- OpenClaw harness: pi-lineage transcripts under `~/.openclaw/agents/*/sessions`, compaction checkpoints and archives skipped; `deja install openclaw` wires `openclaw.json`, doctor gains store and wiring rows. (#312, #320)
- Tags on curated notes (`deja remember --tag`, `deja promote --tag`) and conflict surfacing when an accepted note covers ground another accepted note already holds. (#309)
- Sessions are findable by when they happened: month, year and year-month land in the index as tokens (`deja "what did we do in may"`), and relative-time phrases ("a week ago", "last month") resolve against the moment of the search. (#323, #325)
- Benchmarks report the official per-evidence recall alongside hit@k, and both harnesses ship a JSONL miss report. (#322)

### Changed
- Search ranking: a session is scored by its best message instead of its whole-transcript sum, sessions covering more distinct query words outrank repetition, natural-language queries fall through junk substring intersections to relevance ranking, and stem forms fold in when the exact word is absent from the corpus. LongMemEval-S hit@1 84.9% / hit@5 94.3%; LoCoMo hit@1 69.8% / hit@5 85.6%. (#317, #318, #322, #324)
- Real questions of three or more words answer with a ranked weak tail instead of silence; bare short queries and bare quoted phrases keep the silence contract. (#322)

### Fixed
- Agent startup no longer pays for indexing: the session-start hook ran a full synchronous index through a garnish lookup — up to ~10s per start with a dirty multi-gigabyte store, now ~0.2s worst case. (#315)
- `Ensure` silently ignored its harness scope: every scoped build ingested all stores. (#319)
- Déjà vu matched dotted terms by their first sub-token (an IP degraded to one octet and fired on unrelated sessions); the visible line now names its trigger terms and `deja log` records them. (#313)
- Newer Gemini CLI chats parsed to zero: message state inside `$set` snapshot lines is read now. (#316)
- A missing bucket shard aborted stem-tier searches instead of meaning "no postings". (#323)

## [0.15.2] - 2026-07-23

### Added
- Public benchmark numbers on the site and README; scoop/winget manifests pinned per release. (#304, #305)
- Parallel harness parsing and redaction on cold builds. (#303)

## [0.15.1] - 2026-07-22

### Fixed
- Déjà vu calibration: identifier-shaped terms only, session-level document frequency, cooldown — no more "you have been here" on every prompt. (#292, #293)
- Codex hook could hang agents that keep stdin open; reads now bound at 300ms, and doctor reports codex-side hook state. (#294)

### Changed
- MCP recall serves a stale snapshot instantly while rebuilds run detached; hook and search latency cut across the board. (#291-#302)

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

[Unreleased]: https://github.com/vshulcz/deja-vu/compare/v0.16.1...HEAD
[0.16.1]: https://github.com/vshulcz/deja-vu/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/vshulcz/deja-vu/compare/v0.15.7...v0.16.0
[0.5.0]: https://github.com/vshulcz/deja-vu/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/vshulcz/deja-vu/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/vshulcz/deja-vu/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/vshulcz/deja-vu/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/vshulcz/deja-vu/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vshulcz/deja-vu/releases/tag/v0.1.0
