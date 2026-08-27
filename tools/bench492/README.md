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

Two lessons from using it, so the next measurement does not relearn them:

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

The read-only counterpart is `internal/index/cjkindex_realcorpus492_test.go`,
behind the `realcorpus492` build tag so a normal `go test ./...` never sees
it. It replays what a real build walks before `indexKeys` sees a byte
(`ParseClaudeFile` -> `preRedactSessions` -> skip-if-empty -> `tokenizedPart`)
and reports the one thing no generator can be reasoned into: the bigram
repetition rate, which is what both dedupe halves are paid for. Measured so
far — `benchJAText`, the fixture the microbenchmarks use, 4.000; this
generator, 1.640; a real 365k-message Chinese store, 1.376 (#492). The
per-message speedups move with it, so quote them against a rate.
