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
`mode: "ctx_resume"`. Set `DEJA_CTX_MCP=1` in the MCP server environment to
advertise the cache modes and their extra arguments. Without it, `tools/list`
retains the original six history modes and schema size, even after CLI cache
use. Explicit cache calls remain callable without advertisement. A miss, stale snapshot, or required gap tells the caller
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
`ctx_*` modes, including `ctx_history`, `ctx_promote`, and `ctx_prune`.

## Freshness and sources

Git HEAD, branch, dirty worktree state, task identity, and caller-supplied
component versions are freshness inputs. `refresh` records a required gap when
metadata changes require renewed validation; it does not claim that an old
conclusion remains true.

Git output and file contents are hashed as streams. Untracked content
fingerprinting is limited to 4,096 entries, 1 MiB per file, and 8 MiB of content
in total; Git still enumerates repository paths and tracked diffs. Symlinks
contribute their target name without following it. A limit, unreadable file,
or incomplete scan produces a `partial:` worktree fingerprint, a stale result,
and a required validation gap even when the partial digest has not changed.
Exclude generated artifacts using Git ignore rules or inspect the relevant
changes before checkpointing; refresh alone cannot verify omitted content.

A checkpoint may declare local source descriptors with a `name`, a `layer`, and
an absolute `path`. Source files are strict State JSON and may own only their
`project`, `task`, `memory`, or `session` layer. Checkpoint hashes the file and
refreshes a changed source while preserving other layers. `--versions
'{"task":"v2"}'` on the CLI or `component_versions` in MCP supplies a version
marker when a client has one. The cache does not poll or synchronize remote
systems.

## Storage and limits

Snapshots are immutable while retained, with small current pointers. Each
write keeps at most the latest 100 snapshots per workspace/task identity.
`deja ctx --cache prune --workspace PATH --task ID --keep 10` can reduce that
history explicitly; MCP uses `ctx_prune` with `keep: 10`. The current snapshot
is always retained and versions continue increasing after pruning. Cleanup
uses the same writer lock as checkpoint/refresh/invalidate/promote, and refuses
to prune corrupt or inaccessible history. Retention is validated before
publishing, but old snapshots are deleted only after the new pointer commits.
A subsequent deletion failure emits a stderr warning without reporting the
saved checkpoint as failed; a later write or explicit prune retries cleanup. `history` and `diff` inspect only
retained snapshots. `invalidate` marks a layer or the whole packet stale; it
also writes a snapshot subject to the same retention limit. `promote --item ID --to project|task|permanent|ephemeral`
changes an item's retention placement while preserving its stable ID and
provenance.

`--budget` and `token_budget` use a conservative rendered-byte limit, not a
model-specific tokenizer. The renderer keeps the objective and required gaps
before lower-priority detail, and rejects a budget too small for the
irreducible packet. Local metrics record resume, refresh, checkpoint, lookup,
gap, conflict, and packet-size counters.

Checkpoint text and materialized source content pass through the existing
`redact.Text` boundary before any snapshot or current pointer is written, and
returned checkpoint/refresh data uses that same sanitized state. Nested
project/test credential values are redacted using their field names as context.
`DEJA_NO_REDACT=1` is the existing explicit opt-out. Operational identity,
source paths, versions, and stable provenance are retained. Redaction is
heuristic, not encryption; older snapshots created without it remain unchanged
until pruned or removed. The cache does not rewrite authoritative source files.

## Acceptance traceability

| Requirement | Regression evidence |
| --- | --- |
| Local resume, identity freshness, invalidation, immutable history, and durable checkpoint validation | `TestCheckpointResumeRefreshAndInvalidate`, `TestBranchSwitchReusesSnapshotAndCreatesValidationGap`, `TestConcurrentCheckpointsHaveUniqueVersions`, and `TestCheckpointRejectsAmbiguousStructuredState` in `internal/ctxcache/cache_test.go` |
| Bounded public packets and lossless checkpoint decoding | `TestCtxCachePublicBudgetPreservesRequiredContext`, `TestCtxCachePublicBudgetValidation`, and `TestCtxCachePublicRejectsLossyCheckpointInput` in `cmd/deja/ctx_cache_acceptance_test.go` |
| Local-source isolation, content-hash removal, provenance, supplied version gaps, and strict source JSON | `TestCtxCacheLocalSourceFixtureRefreshesOnlyTask` with `fixtures/synthetic/ctx/task-in-progress.json` and `fixtures/synthetic/ctx/task-validated.json`; `TestSourceAcceptanceRefreshPreservesOtherLayersAndProvenance`, `TestSourceAcceptanceSourceRemovalAndManualVersionRequireValidation`, and `TestSourceAcceptanceRejectsMalformedOwnershipUnknownAndDuplicateJSON` in `internal/ctxcache/source_acceptance_test.go` |
| Explicit conflicts, semantic diffs, explainability, promotion, and local metrics | `TestSourceAcceptanceConflictsPromotionDiffAndExplain` and `TestSourceAcceptanceMetricsRecordActualOperations` in `internal/ctxcache/source_acceptance_test.go` |
| Checkpoint/source redaction, opt-out, and collision rejection | `TestCacheWritersPersistAndReturnRedactedSnapshots`, `TestRedactSnapshotRedactsAgentTextWithoutChangingOperationalMetadata`, and `TestCheckpointRejectsConflictCandidatesThatCollideAfterRedaction` in `internal/ctxcache/redaction_test.go` |
| Bounded history and public pruning | `TestCheckpointHistoryIsBoundedByDefaultRetention` in `internal/ctxcache/prune_test.go`; `TestCtxPrunePublicCLIAndMCP` and `TestCtxPruneRejectsInvalidRetention` in `cmd/deja/ctx_review_test.go` |
| Zero cache schema overhead by default | `TestCtxMCPAdvertisingRequiresExplicitOptIn` and `TestMCPToolsListStaysWithinItsTokenBudget` in `cmd/deja` |
| Bounded untracked scans with honest partial freshness | `TestBoundedWorktreeDigestMarksOversizeFilePartial`, `TestBoundedWorktreeDigestMarksTotalContentLimitPartial`, and `TestBoundedWorktreeDigestMarksEntryLimitPartial` in `internal/ctxcache/worktree_bounded_test.go` |
