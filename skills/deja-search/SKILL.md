---
name: deja-search
description: deja-vu memory — search the user's past AI coding sessions with the deja CLI. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.
metadata:
  openclaw:
    homepage: https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html
    requires:
      bins:
        - deja
    install:
      - kind: brew
        formula: deja-vu
        bins: [deja]
      - kind: node
        package: "@vshulcz/deja-vu"
        bins: [deja]
---

Search deja before re-deriving past work: when the user refers to earlier sessions or decisions, before debugging an error, and before implementing something that may already exist. It searches this machine's own history across every AI coding tool used on it, going back further than deja itself was installed.

This skill drives the `deja` binary through the shell. If the deja MCP tools (recall, recall_context, blame, fix, how, remember) are available in this session, use those instead — same index, one less hop. They appear only when `deja install` has wired this harness.

## Finding something

- `deja search --json "<query>"`: the most specific token available — an exact error string, function name, file path, or flag. Several words are ANDed. Only this user's own sessions, never library docs or general knowledge.
- `deja ctx <query|id-prefix>`: a full digest of the single best-matching session, once a hit looks right and the reasoning behind it matters. Takes no flags.
- `deja show <id-prefix> --harness <name> --json`: the turns themselves, paged with `--offset` and `--limit`. Use the id and harness a hit printed.
- `deja blame <path> --json`: before editing, refactoring or deleting a file, the prior sessions that discussed it, so you know why it is shaped the way it is. Session history, not git authorship.
- `deja fix "<pasted error>"`: the commands that followed that same error before, in sessions where it did not come back. Paste the failing output verbatim.
- `deja how <what>`: the real command with the real flags this machine runs for a build, test, deploy or script, ordered by how many sessions ran it. A guessed invocation is plausible and fails on this setup.
- `deja remember "<text>"`: store one durable decision after it is settled, as a single self-contained fact. Not transcripts, not anything already obvious from the code.

Useful flags on search: `--harness`, `--project`, `--since 30d`, `--role user|assistant|tool|files|command|edit`, `--session <id>`, `--limit 1-100`, `--all`, `--re` for a regular expression.

## Reading a result

`--json` returns an envelope: `tier`, `total`, `capped`, `hits`.

- `tier: "relevance"` means **nothing matched** — those are the nearest sessions deja could find, and counting them as hits overstates what is there. `tier: "error"` IS a match: the query was an error and those sessions hit it, matched by signature rather than by words.
- `total` is how many sessions matched; `capped` says a cap hid some of them. Read those two for coverage, never the length of `hits`.
- `policy_withheld`, when present, is how many matching sessions this machine's trust policy kept out of the answer — an empty result and a rule are different answers.
- A hit may carry `superseded` with a date: the user's own later judgement on that session. Do not repeat a rejected approach, prefer a replacement over what it replaced, and treat a stale result as needing confirmation before acting on it. A hit without it carries no judgement either way.

## Saying what you used

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — tell the user in one short line what was recalled and how you used it: "deja-vu recalled: we hit this JWT skew in March — reusing that fix". Say nothing about recalls that did not help. This is provenance, not advertising; a note on every call would be noise.

## Limits worth respecting

- Result windows are bounded. Do not report corpus-wide counts, or claim a complete audit, from the number of hits you got back.
- If `deja` is not on PATH or the index is empty, say that history search is unavailable. Do not invent what it might have found.
- Work a subagent did is not in the index by default. A Claude Task or a Cursor subagent writes its turns and tool calls to its own transcript, and the parent session keeps only the launch and a summary of what came back — so a hit on the parent can look complete while the actual run is missing. `DEJA_INCLUDE_SUBAGENTS=1` takes those transcripts in, as sessions of their own.
- Vary the wording and try a second query before concluding nothing is there. Exact tokens match best, so an error string beats a paraphrase of it.
