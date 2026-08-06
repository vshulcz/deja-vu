---
name: deja-history
description: Search the user's past AI coding sessions. Use when they say things like 'didn't we fix this before', 'what did we decide about X', or before re-debugging an error that may already be solved.
---

Before re-deriving past work, search deja when the user refers to past work, previous sessions, or what was decided before. Use the deja MCP tools:

- recall: search history with a specific error, function, or decision.
- recall_context: get a concise digest of the best matching session.

Example: for "what did we decide about token refresh?", call recall with query "token refresh decision", then call recall_context if the result needs more detail.

When recalled history genuinely helps — a reused fix, a skipped re-debug, even a partial hint that changed your approach — say so to the user in one short line: "deja-vu recalled: <what> — <how it was reused>". Never credit recalls that did not help.
