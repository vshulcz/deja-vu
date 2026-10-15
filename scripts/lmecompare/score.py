"""Turn a results_*.jsonl from run_cognee.py into one submission per search type
and score each with the repo harness (scripts/longmemeval -score), restricted to
the questions that finished.

usage: score.py results_chunks.jsonl
Env: BENCH_DIR (work dir, default cwd), BENCH_SAMPLE (default sample20.json),
LME (a built scripts/longmemeval binary; default `go run` from this checkout).
"""
import json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
WORK = os.path.abspath(os.environ.get("BENCH_DIR", os.getcwd()))
LME = [os.environ["LME"]] if os.environ.get("LME") else ["go", "run", "./scripts/longmemeval"]

rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
rows = [r for r in rows if r.get("search")]
sample = {q["question_id"]: q for q in json.load(open(os.path.join(WORK, os.environ.get("BENCH_SAMPLE", "sample20.json"))))}
done = [r["question_id"] for r in rows]
subset = os.path.join(WORK, "scored_subset.json")
json.dump([sample[i] for i in done], open(subset, "w"))
stem = os.path.splitext(os.path.basename(sys.argv[1]))[0]
for st in sorted({k for r in rows for k in r["search"]}):
    sub = {r["question_id"]: r["search"].get(st, {}).get("ranked", []) for r in rows}
    p = os.path.join(WORK, f"sub_{stem}_{st}.json")
    json.dump(sub, open(p, "w"))
    print(f"### {stem} {st}  (n={len(done)})")
    out = subprocess.run(LME + ["-data", subset, "-score", p], capture_output=True, text=True, cwd=REPO)
    print(out.stdout[-1200:], out.stderr[-400:])
    for r in rows:
        ranked = r["search"].get(st, {}).get("ranked", [])
        rank = next((i + 1 for i, s in enumerate(ranked) if s in r["answer_ids"]), 0)
        print(f"  {r['question_id']} rank={rank} ingest={r.get('add_s', 0) + r.get('cognify_s', 0):.1f}s "
              f"query={r['search'].get(st, {}).get('s', 0):.2f}s returned={len(ranked)}")
