# lmecompare: other memory systems on LongMemEval-S

Drivers that run another system over LongMemEval-S questions and hand its
ranking to `scripts/longmemeval -score`, so it is scored by the same arithmetic
as deja's own rows. See `docs/benchmarks/SUBMISSION.md` for the file format.

## cognee

Measured 2026-10-15 with cognee 1.6.4, everything local through Ollama
(`llama3.1:8b`, `nomic-embed-text`) on an Apple M4. The numbers are on the
site's compare page.

1. The sample is the first 20 non-abstention questions of
   `longmemeval_s_cleaned.json`; their ids are in `cognee-sample20.txt`. The
   full sample is ~10 MB, so it is not committed. Write it from the dataset
   (the script checks the ids):

       mkdir -p /tmp/lme-cognee
       python3 scripts/lmecompare/sample.py longmemeval_s_cleaned.json > /tmp/lme-cognee/sample20.json

2. The deja row on the same questions:

       go run ./scripts/longmemeval -skip-abs -limit 20 -data longmemeval_s_cleaned.json

3. cognee, in a venv with `cognee==1.6.4` and Ollama running with both models
   pulled. Storage, results and the sample all live in `BENCH_DIR`:

       export BENCH_DIR=/tmp/lme-cognee
       python3 scripts/lmecompare/run_cognee.py 0 20 --mode chunks
       python3 scripts/lmecompare/score.py $BENCH_DIR/results_chunks.jsonl

   `--mode chunks` is cognee's `cognify` with the graph-extraction task
   removed: its chunker and embeddings, no LLM. `--mode full` is the default
   `cognify` (graph extraction and chunk summaries by the LLM); `--budget`
   caps it per question. `--mode estimate` runs `cognify(dry_run=True)` and
   records chunk, call and token counts without calling the model.

Each haystack session goes in as one document; result nodes are mapped back to
session ids through the graph's own provenance edges (chunk `is_part_of`
document, summary `made_from` chunk, chunk `contains` entity). LLM calls are
serialized because Ollama serves one at a time and cognee's parallel
extraction otherwise times out and retries; that changes scheduling, not what
cognee asks the model.
