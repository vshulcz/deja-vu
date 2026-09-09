# Experimental local context cache

`deja ctx --cache ACTION` is an explicit, local opt-in cache for structured
working state. It is separate from Deja's default history and recall workflow: `deja install`
does not add a context hook, and agents are not instructed to resume or
checkpoint automatically. A person or client starts this workflow by calling a
`deja ctx --cache ACTION` command or an MCP `ctx_*` mode. Bare
`deja ctx <query>` remains the historical session digest.

Snapshots live beside the selected history index in a `ctx` directory
(`~/.cache/deja/ctx` by default), or the directory named by `DEJA_CTX_DIR`.
A custom `DEJA_INDEX_DIR` changes the default sibling location. They are local filesystem data, not a history search index,
remote source of truth, or standing-instruction system.

## Explicit lifecycle

Use `deja ctx --cache resume --workspace PATH` and, when relevant, `--task ID` to read
the newest bounded packet. MCP clients can use the `deja` tool with
`mode: "ctx_resume"`. A miss, stale snapshot, or required gap tells the caller
what still needs validation; it does not trigger automatic history search.

Use `deja ctx --cache checkpoint --workspace PATH` after the caller has chosen to save
structured state. The checkpoint JSON may contain an objective, project and
task items, decisions, tests, evidence, gaps, conflicts, next actions, and
provenance. It is agent-authored local state. Do not place secrets or private
reasoning in it.

`--cache status`, `--cache refresh`, `--cache diff`, `--cache history`,
`--cache explain`, `--cache invalidate`, and `--cache promote` inspect or update
that explicit workflow. `--cache lookup` is the only cache action that crosses
into historical retrieval. The MCP equivalents are the matching
`ctx_*` modes, including `ctx_history` and `ctx_promote`.

## Freshness and sources

Git HEAD, branch, dirty worktree state, task identity, and caller-supplied
component versions are freshness inputs. `refresh` records a required gap when
metadata changes require renewed validation; it does not claim that an old
conclusion remains true.

A checkpoint may declare local source descriptors with a `name`, a `layer`, and
an absolute `path`. Source files are strict State JSON and may own only their
`project`, `task`, `memory`, or `session` layer. Checkpoint hashes the file and
refreshes a changed source while preserving other layers. `--versions
'{"task":"v2"}'` on the CLI or `component_versions` in MCP supplies a version
marker when a client has one. The cache does not poll or synchronize remote
systems.

## Storage and limits

Snapshots are immutable history plus small current pointers. `history` shows
older snapshots; `invalidate` marks a layer or the whole packet stale but does
not erase that history. `promote --item ID --to project|task|permanent|ephemeral`
changes an item's retention placement while preserving its stable ID and
provenance.

`--budget` and `token_budget` use a conservative rendered-byte limit, not a
model-specific tokenizer. The renderer keeps the objective and required gaps
before lower-priority detail, and rejects a budget too small for the
irreducible packet. Local metrics record resume, refresh, checkpoint, lookup,
gap, conflict, and packet-size counters.

## Acceptance traceability

| Requirement | Regression evidence |
| --- | --- |
| Local resume, identity freshness, invalidation, immutable history, and durable checkpoint validation | `TestCheckpointResumeRefreshAndInvalidate`, `TestBranchSwitchReusesSnapshotAndCreatesValidationGap`, `TestConcurrentCheckpointsHaveUniqueVersions`, and `TestCheckpointRejectsAmbiguousStructuredState` in `internal/ctxcache/cache_test.go` |
| Bounded public packets and lossless checkpoint decoding | `TestCtxCachePublicBudgetPreservesRequiredContext`, `TestCtxCachePublicBudgetValidation`, and `TestCtxCachePublicRejectsLossyCheckpointInput` in `cmd/deja/ctx_cache_acceptance_test.go` |
| Local-source isolation, content-hash removal, provenance, supplied version gaps, and strict source JSON | `TestCtxCacheLocalSourceFixtureRefreshesOnlyTask` with `fixtures/synthetic/ctx/task-in-progress.json` and `fixtures/synthetic/ctx/task-validated.json`; `TestSourceAcceptanceRefreshPreservesOtherLayersAndProvenance`, `TestSourceAcceptanceSourceRemovalAndManualVersionRequireValidation`, and `TestSourceAcceptanceRejectsMalformedOwnershipUnknownAndDuplicateJSON` in `internal/ctxcache/source_acceptance_test.go` |
| Explicit conflicts, semantic diffs, explainability, promotion, and local metrics | `TestSourceAcceptanceConflictsPromotionDiffAndExplain` and `TestSourceAcceptanceMetricsRecordActualOperations` in `internal/ctxcache/source_acceptance_test.go` |
