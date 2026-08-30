# bench492 — CJK bigram build-cost harness

The measurement rig behind the numbers in #492: a deterministic corpus
generator and an interleaved A/B runner for full builds. Everything runs
against isolated `DEJA_INDEX_DIR` / `DEJA_CLAUDE_ROOT` paths and a fake HOME,
so a real store on the machine is neither read nor touched.

```
python gen_corpus.py --out /tmp/bench --sessions 5000 --projects 50
go build -o deja-a ./cmd/deja        # baseline commit
go build -o deja-b ./cmd/deja        # comparison commit (or a stubbed build)
python run_bench.py --out-root /tmp/bench --bin base=./deja-a --bin opt=./deja-b --rounds 7
```

`--bin LABEL=PATH` is repeatable, so more than two builds can share one
interleaved window. That is the only way to compare more than two: separate
runs are not comparable, for the reason in the first lesson below. The first
`--bin` is the baseline the summary subtracts from, and `--head-bin` /
`--stub-bin` still work as shorthand for `head=` / `stub=`.

`results.csv` holds one row per (round, arm, binary); `results-summary.txt`
reports min/median per cell. Interleaving keeps a machine-wide slowdown from
landing on one cell; read the min column first, contention only ever adds.

Three lessons from using it, so the next measurement does not relearn them:

- Wall-clock deltas between two *different binaries* carry binary-layout
  noise — around ±15% of a small corpus's build time on the machine this was
  built on, measured by comparing two builds of identical source. Effects
  well above that (the bigram on/off arms) are safe to read; single-digit
  percentages are not. For those, put both implementations in one test
  binary and benchmark them side by side; `allocs/op` is immune entirely.
- To measure against a real store instead of the generated one, point
  `--out-root` at a directory whose `store-<arm>/claude-root` is a copy of
  (or junction to) the real transcripts. Keep `DEJA_INDEX_DIR` isolation —
  the runner never writes into a live index either way. Take that copy
  before the first build: a live store grows while the run is going, and
  then the cells are timing different bytes.
- The stub arm has to be a *build* with the `cjkIndexKeys` call in
  `indexKeys` removed, not a runtime switch. `run_bench.py` only measures —
  it never compiles anything, it runs `<bin> index --rebuild` for each cell —
  so any difference between arms has to be baked into the binary handed to
  `--bin` before the run starts. A build was lost to learning this.

## What the pass costs

Whole-build, both corpora, so the numbers can sit next to each other.

**A real store**, `main` at `9ed8498` (`v0.19.0` plus 28 commits). Mac mini,
Apple silicon, macOS, `GOMAXPROCS=2`; frozen APFS clone of 5,289 files /
5,170 sessions / 372,048 indexed messages, 55.4% of messages carrying CJK,
16.6% of runes CJK, repetition rate 1.378. Two windows of 7 rounds, min of 14:

| | min of 14 |
|---|---|
| bigram-free build (`main` with the emitter stubbed) | 87.995 s |
| **bigram cost** | **+4.289 s** |
| as a share of the bigram-free build | 4.87% |
| as a share of the build it is in | 4.65% |
| `-trimpath` noise floor, same source | **0.250 s (0.27%)** |
| effect / noise | **17.2×** |

**A generated all-CJK corpus**, 1,200 sessions per arm (not the 5,000 the
example above uses), 5 interleaved rounds, min:

| arm | bigrams on | off | cost |
|---|---|---|---|
| ja | 1.383 s | 0.822 s | +0.561 s, 68% of the bigram-free build |
| en | 0.294 s | 0.316 s | none, which is the control working |

Index size on the `ja` arm: 14.0 MB against 10.6 MB.

So the bracket is **4.87% on a real store where CJK is 16.6% of runes, and
68% on an all-Japanese generated one** — same code, two corpora, and the
answer for any given store sits between them. Neither number alone is the
answer to "what does the bigram pass cost"; the pair is.

### Why the summary carries its own noise floor

The real-store run above took two windows, and the `-trimpath` cell is the
only thing that says which one to believe:

| | window 1 | window 2 |
|---|---|---|
| bigram cost | +5.814 s | +4.289 s |
| `-trimpath` spread, identical source | 1.337 s | 0.250 s |
| effect / noise | 4.4× | 17.2× |

Window 1's noise floor was 5× window 2's, and its per-round `main − stub`
deltas changed sign twice — two rounds had the stubbed build coming out
*slower*, which it cannot be: its index is 114.3 MB smaller. Window 1 was
measuring the machine. Published, it would have been 36% too high, on a rig
whose own first lesson says not to.

Window 2's floor of 0.250 s is also the strongest available statement that
the run was clean: an earlier real-store run on `v0.19.0`, weeks apart in a
separate window on the same machine, measured 0.249 s for the same layout
spread. Both windows are kept here on purpose — a summary showing only the
surviving number teaches the wrong lesson about what the third cell is for.

The read-only counterpart is `internal/index/cjkindex_realcorpus492_test.go`,
behind the `realcorpus492` build tag so a normal `go test ./...` never sees
it. It replays what a real build walks before `indexKeys` sees a byte
(`ParseClaudeFile` -> `preRedactSessions` -> skip-if-empty -> `tokenizedPart`)
and reports the one thing no generator can be reasoned into: the bigram
repetition rate, which is what both dedupe halves are paid for. Measured so
far — `benchJAText`, the fixture the microbenchmarks use, 4.000; this
generator, 1.640; a store where CJK is incidental rather than the language of
the work (635 of 205,490 messages carry any), 1.685; a real 365k-message
Chinese store, 1.376 (#492). The per-message speedups move with it, so quote
them against a rate. The generator models the incidental case well and the
native case is the low end, which is the direction that matters: the dedupe
halves are worth least exactly where the text is most CJK.
