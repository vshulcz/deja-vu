---
description: Search local AI coding session history or orient on the current project using deja.
---

Use the `deja` MCP tool:

- If `$ARGUMENTS` is empty, call `deja` with `mode: "orient"` for the current repository.
- Otherwise, call `deja` with `mode: "recall"` and `q: "$ARGUMENTS"`.

Summarize relevant past sessions concisely with session IDs and file paths.
