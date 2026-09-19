# Scoring your own retrieval on LongMemEval-S

Every memory tool publishes a number from its own harness, so no two numbers
compare. This is the other half of the harness in `scripts/longmemeval`: it
scores a ranking **your** system produced, with the same arithmetic that
produces deja's published figures.

Nothing in here runs deja. No index is built, no store is read — the dataset and
your file are all it takes, and it finishes in seconds.

## The file

JSON, mapping the dataset's `question_id` to the session ids your system ranked,
best first. Twenty is enough; more is ignored.

```json
{
  "gpt4_e5c7d0f1": ["answer_session_3", "haystack_session_11", "haystack_session_2"],
  "gpt4_9a1b2c3d": ["haystack_session_7", "answer_session_1"]
}
```

A wrapper key is accepted too, because refusing a file over that wastes an
afternoon and teaches nothing:

```json
{ "answers": { "gpt4_e5c7d0f1": ["answer_session_3"] } }
```

## Running it

```sh
go run ./scripts/longmemeval -data longmemeval_s.json -score yours.json
go run ./scripts/longmemeval -data longmemeval_s.json -score yours.json -out yours-result.json
```

The first prints the table. The second also writes the artifact shape this
repository publishes its own runs in, so your number arrives with its dataset
digest, its per-type rows and the metric definition attached rather than as a
sentence in a README.

```
LongMemEval-S · submitted ranking from yours.json
questions: 500 · dataset longmemeval_s_cleaned.json · sha256 d6f21ea9…

type                              n    hit@1    hit@5   hit@10   hit@20      MRR
knowledge-update                 72    94.4%    98.6%    98.6%    98.6%   0.965
…
TOTAL                           500    84.8%    95.6%    96.4%    97.2%   0.894
evidence-recall (official)             54.3%    86.0%    88.7%    91.3%
```

## The rules, such as they are

- **Any method.** Embeddings, an LLM rewriting the query, a graph, grep. Say
  which when you publish the number; the scorer does not care and the reader
  does.
- **hit@k** credits a question when any of its evidence sessions reaches the top
  k. **evidence-recall** is the stricter per-evidence metric the dataset itself
  defines, and it is reported beside it.
- **A question with no ranking in your file counts as a miss**, and the run says
  how many. A submission that answers half the set and scores well on that half
  is the most common way a benchmark gets misread.
- **The ids must be the dataset's own** session ids. That is what makes two
  files comparable.
- `-skip-abs` drops the abstention questions, which is what the cleaned-set
  numbers in this repository use. Scoring does not need the flag — your file
  decides which questions it answers — but say which set you are quoting,
  because 470 questions and 500 give different numbers for the same system.

## deja's own numbers, for the same dataset

| set | hit@1 | hit@5 | hit@10 | hit@20 | MRR |
|---|---|---|---|---|---|
| 470 questions, `-skip-abs` | 85.3% | 95.5% | 96.4% | 97.2% | 0.898 |
| all 500 | 84.8% | 95.6% | 96.4% | 97.2% | 0.894 |

Both runs are committed beside this file — `longmemeval-s-cleaned.json` and
`longmemeval-s-full.json` — with the dataset's sha256, the flags, the wall time
and the per-type rows. deja's retrieval is lexical: no LLM, no embeddings, no
query rewriting.

## If your number is better

Open a pull request against this file's table, or an issue with the artifact.
A row that beats ours is worth more to whoever is choosing a tool than another
paragraph from either of us, and the driver is here so nobody has to take
either side's word for it.
