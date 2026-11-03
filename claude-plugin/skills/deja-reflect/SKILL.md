---
name: deja-reflect
description: End-of-session reflection and memory curation with deja-vu. Review what was decided, what commands broke and what repaired them, and record durable project rules or decisions with deja remember so the next session starts with them.
---

Run an end-of-session reflection over the current task and record only durable, non-obvious project facts in deja-vu.

## Workflow

1. **Inspect what was already recorded or hit in this project:**
   - Call the `deja` tool with `mode: "orient"` (or run `deja notes` and `deja friction` in the shell) to see existing promoted decisions and recurring walls so you do not duplicate an active note.
2. **Distill durable findings from this session:**
   - Keep: architectural invariants, required flags/commands that were non-obvious, rejected approaches and why they failed, user-stated project rules.
   - Skip: anything already obvious from reading the code, temporary debugging steps, or one-off typos.
3. **Confirm with the user before writing:**
   - Present the 1–3 concise one-line facts you propose to store.
   - Once confirmed, call the `deja` tool with `mode: "remember"` and `query: "<concise self-contained decision and why>"` (or run `deja remember "<fact>"` in the shell).
   - If an older note was superseded by today's work, run `deja promote <id> --state superseded --note "<what replaced it>"`.
