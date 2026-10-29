# Roadmap

Direction, not release commitments. What ships moves into the changelog; what is
here is where the work is going. Linked issues carry the current scope and are
the place to discuss design.

## Now

- **Answer-bearing blocks at every seam.** The session-start digest, the
  per-prompt recall and the point-of-action tool hooks are judged on whether
  the injected block carries the settled conclusion rather than just a matching
  word. That is measured on the prompt and block benchmarks, on LongMemEval-S
  end to end, and against real multi-harness stores; every change ships with
  the before and after.
- **Sub-second incremental indexing on 50k+ session stores.** Rewritten
  transcripts already append in place; the remaining work is keeping renames
  and multi-session SQLite stores (opencode, Cursor, Kilo, ZCode, goose)
  proportional to the delta rather than the store size.
- **Cross-platform parity across macOS, Linux and Windows.** Path handling,
  store relocation, rebuild exclusions and native launcher wiring are verified
  hermetically across all three operating systems.
- **Supply-chain and privacy guarantees.** Maintain the redaction boundary,
  trust policy (`policy.json`), signed checksums, provenance and release SBOMs
  as upstream harness formats evolve.

## Next

- **Compaction recovery across the remaining hosts.** The recovery packet after
  context compaction now reaches 34 of the 43 harnesses — either through a
  native `PreCompact` hook or by catching up from the store's pre-summary turns
  on the next hook that reaches the model. What remains is wiring the hosts
  whose compaction seams are still landing upstream (`#308`).
- **Pre-execution plan checks.** `deja check` answers a proposed plan with what
  this machine already settled or reverted on the same files, and `hook-plan`
  delivers it before execution. Wiring it by default waits on measuring how
  often the warning changes a flawed plan without adding noise on clean ones.
- **Query-scoped reuse.** Reuse is a global signal today: a session recalled
  for one topic receives a small lift everywhere. Recording which terms a
  recall served lets the lift apply only when the same subject returns.
- **Point-of-action coverage.** Prior file decisions before an edit reach 31 of
  the 43 harnesses and failure repairs reach 30. As upstream harnesses add
  pre/post tool hooks or plugin middleware, each is wired and recorded in the
  format registry with a reproducible stand check.

## Later

- **Optional semantic sidecar (`deja embed`).** Off by default and external to
  the binary, the vector sidecar reranks lexical hits and answers when a query
  shares no exact tokens with the session (cross-language and paraphrased
  queries, `#4976`).

## Not planned

- **Capture daemons.** deja indexes the histories each harness already writes; a
  background recorder would add a persistent process and a second source of
  truth.
- **Cloud sync by default.** Implicit upload conflicts with local-only indexing;
  sync stays an explicit export or an SSH operation you run.
- **Embeddings in the base binary.** Model weights and vector runtimes would
  break the zero-runtime-dependency distribution and inflate index cost. The
  semantic tier stays opt-in and external.
- **Enforcing recalled conclusions.** deja stays at the evidence layer. Curation
  — `promote`, lifecycle states, standing project decisions — makes a decision
  durable and surfaces when it was reverted or superseded, but the agent
  decides.
