# Write the scored sample: the first N non-abstention questions of
# LongMemEval-S cleaned, in dataset order -- the same questions
# `go run ./scripts/longmemeval -skip-abs -limit N` scores. Checks the ids
# against cognee-sample20.txt when N is 20.
#
# usage: sample.py longmemeval_s_cleaned.json [N] > sample20.json
import json, os, sys

n = int(sys.argv[2]) if len(sys.argv) > 2 else 20
qs = [q for q in json.load(open(sys.argv[1])) if not q["question_id"].endswith("_abs")][:n]
if n == 20:
    want = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "cognee-sample20.txt")).read().split()
    if [q["question_id"] for q in qs] != want:
        sys.exit("question ids differ from cognee-sample20.txt: wrong dataset file?")
json.dump(qs, sys.stdout)
