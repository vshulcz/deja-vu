# Roadmap

Direction, not release commitments. What ships moves into the changelog; what is
here is where the work is going. Linked issues carry the current scope and are
the place to discuss design.

## Now

- **Does the block carry the answer.** The per-prompt hook is judged on one
  question now: whether what it injects holds what the question was about, not
  whether it matched a word. That is measured three ways — the prompt benchmark
  on a corpus large enough for rarity to mean something, LongMemEval end to end,
  and a replay of real prompts against a frozen index — and every change ships
  with the before and after. The next piece is the same treatment for the block
  a tool call gets, where the reader is an agent about to run something.
- **Windows correctness.** The Windows leg runs on `main`, on the weekly canary
  and on any pull request labelled `windows` rather than on every commit, so the
  gap between "green on Linux and macOS" and "correct on Windows" has to be
  closed deliberately. Path-shaped fixtures and the exclude-on-rebuild path are
  verified there; anything touching paths, output or the filesystem gets the
  label.
- Maintain the security model, signed checksums, provenance and release SBOMs as
  release and harness formats change.

## Next

- **Deepen curation past a single boost.** Reuse is a global signal today: a
  session pulled for one query is lifted for every query. A per-query signal —
  recording what a recall was for, not only that it happened — would let reuse be
  both stronger and precise without lifting an off-topic session.
- **Point-of-action beyond codex and Claude.** Carry the file's or command's
  prior decision into other harnesses as their hook contracts allow.
- **Follow the work an agent handed off.** A subagent's run is its own session
  now, and where a harness records the edge — Grok's `summary.json`, Claude's
  sidechain files — recall can name the parent and the children. Cursor writes
  subagent transcripts too and their shape is unread; the rest of the harnesses
  that spawn agents are the same question.
- **Close the matrix from upstream.** What is left in the support table is
  someone else's to ship: aider has no MCP client and no custom commands, Roo's
  hooks are in flight, Zed exposes no lifecycle hook. Each becomes work the day
  it lands, and the registry records the source so the claim can be rechecked
  rather than assumed.

## Later

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
