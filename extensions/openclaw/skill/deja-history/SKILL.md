---
name: deja-history
description: Search the user's past AI coding sessions across the twenty-one AI coding agents this machine may run — Claude Code, Codex, Cursor and OpenClaw among them. Use when they say 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.
version: 0.19.2
metadata:
  openclaw:
    emoji: "🐈"
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

Search deja before re-deriving past work: when the user refers to earlier sessions or decisions, before debugging an error, and before implementing something that may already exist. It searches this machine's own history across every AI coding tool used on it — twenty-one of them, OpenClaw included — going back further than deja itself was installed. Nothing leaves the machine.

Once the binary is on PATH, `deja install openclaw-auto` adds the MCP server, a hook pack that puts a digest of relevant history into the Project Context at bootstrap, and a plugin that answers `before_prompt_build` on each prompt. With only this skill installed, the same index is reachable through the shell.

## Finding something

- `deja search --json "<query>"`: search with the most specific token available — an exact error string, function name, file path, or flag. Several words are ANDed. Not for library docs or general knowledge; only this user's own sessions.
- `deja ctx <query>`: a full digest of the single best-matching session, once a hit looks right and the reasoning behind it matters.
- `deja blame <path> --json`: before editing, refactoring or deleting a file, the prior sessions that discussed it, so you know why it is shaped the way it is. Session history, not git authorship.
- `deja fix "<failing output>"`: paste a failing output verbatim to see the commands that followed that same error before, in sessions where it did not come back.
- `deja how "<command>"`: the real command with the real flags this machine runs for a build, test, deploy or script, ordered by how many sessions ran it. A guessed invocation is plausible and fails on this setup.
- `deja remember "<fact>"`: store one durable decision after it is settled, as a single self-contained fact. Not transcripts, not anything already obvious from the code.

When the MCP server is installed, the same six are tools: `recall`, `recall_context`, `blame`, `fix`, `how`, `remember`.

## Reading a result

A result may carry a bracketed marker with a date — that is the user's own later judgement on that session, and it is not advisory. Do not repeat a rejected approach. Prefer a replacement over what it replaced. Treat a stale result as needing confirmation before acting on it. A result with no marker carries no judgement in either direction.

## Saying what you used

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — tell the user in one short line what was recalled and how you used it: "deja-vu recalled: we hit this JWT skew in March — reusing that fix". Say nothing about recalls that did not help. This is provenance, not advertising; a note on every call would be noise.

## Limits worth respecting

- Result windows are bounded. Do not report corpus-wide counts, or claim a complete audit, from the number of hits you got back.
- If deja is unavailable or the index is empty, say that history search is unavailable. Do not invent what it might have found.
- Vary the wording and try a second query before concluding nothing is there. Exact tokens match best, so an error string beats a paraphrase of it.
