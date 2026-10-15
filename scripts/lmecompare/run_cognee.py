"""Per question: reset cognee, add each haystack session as one document, cognify
with the local Ollama model, search, map results back to session ids through the
graph's own provenance edges (summary -made_from-> chunk -is_part_of-> document,
chunk -contains-> entity).

usage: run_cognee.py <start> <count> [--mode full|chunks|estimate] [--budget SECONDS]
  full      default cognify (LLM graph extraction + LLM chunk summaries)
  chunks    cognify minus the LLM task: cognee's chunker + embeddings only
  estimate  cognify(dry_run=True): chunk / LLM-call / token counts, no LLM
Env: BENCH_DIR (work dir, default cwd), BENCH_SAMPLE (default sample20.json,
from sample.py), RESULTS (output basename), BENCH_LLM, OLLAMA_HOST_URL.
"""
import asyncio, json, os, sys, time, re, traceback

import env  # noqa: F401  (sets local-only config before cognee import)
import cognee
from cognee import SearchType
from cognee.tasks.ingestion.data_item import DataItem

WORK = env.WORK
BUDGET = 15 * 60
MODE = "full"
args = sys.argv[1:]
for flag in ("--budget", "--mode"):
    if flag in args:
        i = args.index(flag)
        if flag == "--budget":
            BUDGET = int(args[i + 1])
        else:
            MODE = args[i + 1]
        del args[i:i + 2]
START, COUNT = int(args[0]), int(args[1])

# Ollama serves one request at a time (OLLAMA_NUM_PARALLEL=1). cognee fires all
# chunk extractions at once, so queued requests hit litellm's 600 s timeout,
# come back 500 and get retried forever. Serialize LLM calls here instead; this
# changes scheduling only, not what cognee asks the model.
import litellm
_acompletion = litellm.acompletion
_sem = None
LLM_CALLS = {"n": 0, "s": 0.0}


async def _serial_acompletion(*a, **kw):
    global _sem
    if _sem is None:
        _sem = asyncio.Semaphore(1)
    async with _sem:
        kw.setdefault("timeout", 1800)
        # cognee keeps LLM_MAX_COMPLETION_TOKENS but never sends it on the
        # structured path; llama3.1:8b then loops inside the JSON for 10k tokens.
        kw.setdefault("max_tokens", int(os.environ.get("LLM_MAX_COMPLETION_TOKENS", "4096")))
        t = time.time()
        try:
            return await _acompletion(*a, **kw)
        finally:
            LLM_CALLS["n"] += 1
            LLM_CALLS["s"] += time.time() - t
litellm.acompletion = _serial_acompletion
OUT = os.path.join(WORK, os.environ.get("RESULTS", f"results_{MODE}") + ".jsonl")

if MODE == "chunks":
    import importlib
    cmod = importlib.import_module("cognee.api.v1.cognify.cognify")
    _orig = cmod.get_default_tasks

    async def _no_llm_tasks(*a, **kw):
        tasks = await _orig(*a, **kw)
        return [t for t in tasks if "extract_graph_and_summarize" not in repr(t.executable)]
    cmod.get_default_tasks = _no_llm_tasks
    SEARCHES = [SearchType.CHUNKS]
else:
    SEARCHES = [SearchType.CHUNKS, SearchType.SUMMARIES, SearchType.GRAPH_COMPLETION,
                SearchType.TRIPLET_COMPLETION]


def session_text(date, turns):
    lines = [f"Session date: {date}"]
    for t in turns:
        lines.append(f"{t['role']}: {t['content']}")
    return "\n".join(lines)


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


class Prov:
    """node id -> ordered session ids, built from the graph after cognify."""

    def __init__(self, q, nodes, edges):
        texts = {norm(session_text(d, t)): sid for sid, d, t in
                 zip(q["haystack_session_ids"], q["haystack_dates"], q["haystack_sessions"])}
        self.doc2sid = {}
        for nid, p in nodes:
            if p.get("type") == "TextDocument":
                loc = (p.get("raw_data_location") or "").removeprefix("file://")
                try:
                    body = norm(open(loc).read())
                except OSError:
                    continue
                self.doc2sid[str(nid)] = texts.get(body)
        chunk2doc, summ2chunk, ent2chunks = {}, {}, {}
        for s, d, rel, _ in edges:
            s, d = str(s), str(d)
            if rel == "is_part_of" and d in self.doc2sid:
                chunk2doc[s] = d
            elif rel == "made_from":
                summ2chunk[s] = d
            elif rel == "contains":
                ent2chunks.setdefault(d, []).append(s)
        self.map = {}
        for c, d in chunk2doc.items():
            self.map[c] = [self.doc2sid[d]]
        for sm, c in summ2chunk.items():
            self.map[sm] = self.map.get(c, [])
        for e, cs in ent2chunks.items():
            out = []
            for c in cs:
                for sid in self.map.get(c, []):
                    if sid not in out:
                        out.append(sid)
            self.map[e] = out
        self.missing_docs = sum(1 for v in self.doc2sid.values() if v is None)

    def sessions(self, node_id):
        return [s for s in self.map.get(str(node_id), []) if s]


def node_ids(res):
    """Ranked node ids from a search result: chunk/summary rows or graph edges."""
    out = []
    for r in res:
        if isinstance(r, dict) and "objects_result" in r:
            for e in r["objects_result"] or []:
                for n in (getattr(e, "node1", None), getattr(e, "node2", None)):
                    if n is not None:
                        out.append(str(n.id))
        elif isinstance(r, dict) and "id" in r:
            out.append(str(r["id"]))
        elif hasattr(r, "search_result"):
            out.extend(node_ids(r.search_result if isinstance(r.search_result, list) else [r.search_result]))
    return out


async def graph():
    from cognee.infrastructure.databases.graph import get_graph_engine
    g = await get_graph_engine()
    return await g.get_graph_data()


def graph_stats(nodes, edges):
    types, rels = {}, {}
    for _, p in nodes:
        types[p.get("type", "?")] = types.get(p.get("type", "?"), 0) + 1
    for e in edges:
        rels[e[2]] = rels.get(e[2], 0) + 1
    ents = [p.get("name") for _, p in nodes if p.get("type") == "Entity"]
    return {"nodes": len(nodes), "edges": len(edges), "node_types": types,
            "edge_types": dict(sorted(rels.items(), key=lambda x: -x[1])[:30]),
            "entity_sample": ents[:80]}


async def one(q):
    rec = {"question_id": q["question_id"], "question": q["question"], "answer_ids": q["answer_session_ids"],
           "mode": MODE, "llm": env.LLM}
    await cognee.prune.prune_data()
    await cognee.prune.prune_system(metadata=True)
    items = [DataItem(data=session_text(d, t), label=sid, literal_text=True)
             for sid, d, t in zip(q["haystack_session_ids"], q["haystack_dates"], q["haystack_sessions"])]
    t0 = time.time()
    await cognee.add(items, dataset_name="lme")
    rec["add_s"] = time.time() - t0
    if MODE == "estimate":
        est = await cognee.cognify(datasets=["lme"], dry_run=True)
        rec["estimate"] = repr(est)[:3000]
        return rec
    LLM_CALLS.update(n=0, s=0.0)
    t1 = time.time()
    try:
        await asyncio.wait_for(cognee.cognify(datasets=["lme"]), timeout=BUDGET)
        rec["cognify_s"] = time.time() - t1
    except asyncio.TimeoutError:
        rec["cognify_s"] = time.time() - t1
        rec["timed_out"] = True
    rec["llm_calls"] = dict(LLM_CALLS)
    nodes, edges = await graph()
    rec["graph"] = graph_stats(nodes, edges)
    if rec.get("timed_out"):
        return rec
    prov = Prov(q, nodes, edges)
    rec["unmapped_docs"] = prov.missing_docs
    rec["search"] = {}
    for st in SEARCHES:
        ts = time.time()
        try:
            kw = {"top_k": 20}
            if st in (SearchType.GRAPH_COMPLETION, SearchType.TRIPLET_COMPLETION):
                kw.update(only_context=True, verbose=True)
            res = await cognee.search(query_text=q["question"], query_type=st, datasets=["lme"], **kw)
            el = time.time() - ts
            ids = node_ids(res)
            ranked = []
            for nid in ids:
                for sid in prov.sessions(nid):
                    if sid not in ranked:
                        ranked.append(sid)
            rec["search"][st.value] = {"ranked": ranked, "n_nodes": len(ids),
                                      "unmapped_nodes": sum(1 for n in ids if not prov.sessions(n)), "s": el}
            if st == SearchType.GRAPH_COMPLETION:
                rec["search"][st.value]["context_head"] = str(res[0].get("context_result", ""))[:2500]
        except Exception as e:
            rec["search"][st.value] = {"err": repr(e)[:800], "s": time.time() - ts}
    return rec


async def main():
    qs = json.load(open(os.path.join(WORK, os.environ.get("BENCH_SAMPLE", "sample20.json"))))[START:START + COUNT]
    for q in qs:
        print(f"== {q['question_id']}", flush=True)
        try:
            rec = await one(q)
        except Exception as e:
            rec = {"question_id": q["question_id"], "err": repr(e)[:2000], "tb": traceback.format_exc()[-3000:]}
        with open(OUT, "a") as f:
            f.write(json.dumps(rec, default=str) + "\n")
        print("RESULT " + json.dumps({k: v for k, v in rec.items() if k not in ("search", "graph", "tb")}, default=str), flush=True)
        if rec.get("timed_out"):
            print("budget exceeded; stopping", flush=True)
            break


asyncio.run(main())
