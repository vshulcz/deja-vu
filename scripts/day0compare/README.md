# day0compare — the first minute after install, tool by tool

`scripts/day0bench` measures deja on a machine that already has history.
These drivers run other tools over the same corpus and score them by the same
rule, so the numbers on the site's day-zero page can be reproduced end to end.

1. Lay the corpus down once, in the real on-disk layouts:

       go run ./scripts/day0bench -data longmemeval_s_cleaned.json -limit 100 -corpus 500 -keep /tmp/day0

   `/tmp/day0/home` then holds `~/.claude/projects` and `~/.codex/sessions`
   trees (19,195 sessions), and `/tmp/day0/questions.json` the 100 scored
   questions with the id of the session each answer lives in. The deja row is
   what that command prints.

2. Run a tool with `HOME=/tmp/day0/home` and its own data directory, so it sees
   the history and nothing else:

       CASS=/path/to/cass python3 scripts/day0compare/cass.py /tmp/day0
       AGENTSVIEW=agentsview KEYWORDS=1 python3 scripts/day0compare/agentsview.py /tmp/day0 --fts
       AGENTMEMORY=/path/to/agentmemory python3 scripts/day0compare/agentmemory.py /tmp/day0
       MEMPALACE=mempalace python3 scripts/day0compare/mempalace.py /tmp/day0
       FUNES=funes python3 scripts/day0compare/funes.py /tmp/day0 [--half-life 0]

3. The standing cost of wiring a tool in at all — its definitions, which a
   client puts in the prompt on every turn — is measured separately, without
   the corpus:

       SERVER='["deja","mcp"]' python3 scripts/day0compare/toolcost.py

   funes indexes the Claude layout in full (`funes index <path> --yes`, about
   two hours of local embedding for 19k sessions); its first recall downloads
   ~1.1 GB of models, and the header shortens session ids, so the driver reads
   the full id from the `→ get` line. agentmemory needs its worker running first (`agentmemory` with
   `HOME`/`AGENTMEMORY_DATA_DIR` set) and imports in batches of at most 1000
   files, its own cap; the driver builds the batches. agentsview searches through
   its daemon; the driver starts it after `agentsview sync` and stops it at the
   end. MemPalace mines the
   Claude layout only — the sessions are the same files under both roots.

The rule is one line: a question scores at rank k when the session holding its
answer appears at position k of the results. hit@1 and hit@5 count ranks 0 and
<5, found@50 counts any rank. Latency is wall time of the search call from a
warm process, `first` the very first call after the build. Nothing is tuned per
tool; each runs with its defaults and its documented import path.
