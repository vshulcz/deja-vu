# Roadmap

Direction, not release commitments. What ships moves into the changelog; what is
here is where the work is going. Linked issues carry the current scope and are
the place to discuss design.

## Now

- **Windows correctness.** Several `internal/index` tests use Unix-shaped path
  fixtures that only pass off Windows by accident, and the exclude-on-rebuild
  path reads paths differently under Windows semantics — so a real privacy
  control is unverified there ([#1119](https://github.com/vshulcz/deja-vu/issues/1119)).
  Close the gap between "green on Linux and macOS" and "correct on Windows," on
  real installations ([#9](https://github.com/vshulcz/deja-vu/issues/9)).
- **Keep the claims measured.** Every ranking signal — outcome, reuse, decision,
  point-of-action — has an in-repo benchmark, and a change ships with the
  before/after. Extend the agent-in-the-loop A/B beyond the point-of-action hook
  to the rest of the recall path.
- Maintain the security model, signed checksums, provenance and release SBOMs as
  release and harness formats change.

## Next

- **Deepen curation past a single boost.** Reuse is a global signal today: a
  session pulled for one query is lifted for every query. A per-query signal —
  recording what a recall was for, not only that it happened — would let reuse be
  both stronger and precise without lifting an off-topic session.
- **Point-of-action beyond codex and Claude.** Carry the file's or command's
  prior decision into other harnesses as their hook contracts allow.
- Derive canonical project identity from a repository remote so same-named repos
  and worktrees do not collide ([#44](https://github.com/vshulcz/deja-vu/issues/44)).

## Later

- Expose recent sessions as an MCP resource alongside bounded recall pagination
  ([#12](https://github.com/vshulcz/deja-vu/issues/12)).
- Mature the optional semantic tier (`deja embed`): still off by default and
  external to the binary, it should degrade and recover as cleanly as the lexical
  ladder does.

## Not planned

- **Capture daemons.** deja indexes the histories each harness already writes; a
  recorder would add a persistent process and a second source of truth.
- **Cloud sync by default.** Implicit upload conflicts with local-only indexing;
  sync stays an explicit export or an SSH operation you run.
- **Embeddings in the base binary.** Model files and vector runtimes would break
  the zero-runtime-dependency distribution and inflate index cost. The semantic
  tier stays opt-in and external.
- **Enforcing recalled conclusions.** deja stays at the evidence layer. Curation
  — promote, lifecycle states, standing project decisions — makes a decision
  durable and surfaces when it was reverted or superseded, but the agent decides.
  deja does not gate behaviour on a past conclusion, and does not claim an old
  conclusion is still true; it shows what was decided and what changed.
