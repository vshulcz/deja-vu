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
  with the before and after. The block a tool call gets has had the same
  treatment now, read off a real store rather than a fixture: what is left there
  is the failure path, where a pair exists for 21% of the failures an agent
  actually hits and the rest are the harness's own refusals, which deja has no
  lever on yet.
- **What a read costs.** One search allocates 701 MB and 57% of it is candidate
  message text copied out of the file to be scored, while the postings already
  carry the offsets of the records that matched. Reading only what is scored, and
  reading it without copying, are the two directions; both touch the hot path, so
  each wants its latency, peak-RSS and bench numbers before and after. The map
  with today's measurements is in
  [#3491](https://github.com/vshulcz/deja-vu/issues/3491).
- **Windows correctness.** The Windows leg runs on `main`, on the weekly canary
  and on any pull request labelled `windows` rather than on every commit, so the
  gap between "green on Linux and macOS" and "correct on Windows" has to be
  closed deliberately. Path-shaped fixtures and the exclude-on-rebuild path are
  verified there; anything touching paths, output or the filesystem gets the
  label.
- Maintain the security model, signed checksums, provenance and release SBOMs as
  release and harness formats change.

## Next

- **Compaction recovery past two hosts.** Claude Code and Codex hand a hook the
  transcript before they shorten it, which is what makes the capture possible.
  Every other harness that compacts keeps the summary to itself, so the packet
  stops at those two until a host exposes the same seam — and each one that does
  is work the day it lands.
- **The plan, before it is executed.** `deja check` answers a plan with what this
  machine already knows about it, and `hook-plan` delivers the same thing at
  `ExitPlanMode`. No installer wires it yet: the measurement that would justify
  it — does an agent change a plan it is about to run — has not been made.
- **Deepen curation past a single boost.** Reuse is a global signal today: a
  session pulled for one query is lifted for every query. A per-query signal —
  recording what a recall was for, not only that it happened — would let reuse be
  both stronger and precise without lifting an off-topic session.
- **Point-of-action in the harnesses that still refuse it.** The repair beside a
  failed command and the file's prior decision now reach Claude Code, Codex,
  Cursor, Gemini, Qwen, Cline, Amp, Antigravity, pi and omp. What is left is
  where the harness itself drops what a hook returns — Kimi's post-tool events,
  prime-agent's tool events, Roo until its hooks ship — and each is recorded in
  the registry with the measurement behind it.
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
