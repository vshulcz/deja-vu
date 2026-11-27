# Roadmap

Engineering direction rather than release commitments; shipped work moves to `CHANGELOG.md`.

## Now

- **Answer-bearing blocks at every seam.** Judge session-start, per-prompt, and tool hooks by whether injected context carries settled conclusions rather than keyword overlap, gated by the prompt/block suites and LongMemEval-S.
- **Sub-second incremental indexing on 50k+ session stores.** Rewritten JSONL transcripts, multi-session SQLite stores, and file or directory renames update in proportion to the delta; keep incremental passes independent of total corpus size as harness formats grow.
- **Cross-platform parity.** Keep path handling, store relocation, rebuild exclusions, and hook launchers hermetic across macOS, Linux, and Windows.
- **Supply-chain and privacy guarantees.** Maintain index-time redaction, trust policy (`policy.json`), signed checksums, build provenance, and release SBOMs as harness formats evolve.

## Next

- **Compaction recovery across remaining hosts.** Recovery reaches 39 of the 53 harnesses (capture/reset in 40) via `PreCompact`/`PostCompaction` hooks or catch-up parsers; remaining hosts depend on upstream hook support (`#308`).
- **Pre-execution plan checks.** `deja check`, `hook-plan`, `hook-tool` (`ExitPlanMode`), and `hook-antigravity` (`exit_plan_mode`) answer proposed plans with project-scoped failure walls, per-file guardrails, and direct coupled-file checks before execution starts.
- **Query-scoped reuse.** Attribute session recall lifts to the matched terms/subject rather than applying a global per-session boost.
- **Point-of-action coverage.** File-history hints and failure remediation reach 42 of the 53 harnesses; remaining targets are wired as upstream hosts expose tool middleware.

## Later

- **Optional semantic sidecar (`deja embed`).** External opt-in vector sidecar for cross-language and paraphrased queries (`#4976`).

## Not planned

- **Capture daemons.** Index host transcripts in place rather than running background recorders.
- **Cloud sync by default.** Keep synchronization explicit via `deja sync export/import/ssh`.
- **Bundled embeddings.** Keep zero runtime weight dependencies; embeddings stay external and opt-in.
- **Silent constraint enforcement.** Surface curated and rejected decisions with provenance while leaving execution control to the agent.
