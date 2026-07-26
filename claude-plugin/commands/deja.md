---
name: deja
description: Search this machine's past AI coding sessions (deja-vu)
---

Search the user's own past sessions across every AI coding tool on this
machine, then answer from what you find.

Run the recall tool with the user's words as the query — the most specific
tokens win (an exact error string, a function name, a file path, a flag).
If a result looks right but is too short to act on, follow up with
recall_context using a term from it.

If the deja MCP tools are unavailable, fall back to the CLI:

```bash
deja "$ARGUMENTS"
```

Answer with what actually happened in those sessions — when it was, which
project and tool, what was decided or fixed. Say plainly if nothing matched
rather than filling the gap from general knowledge.
