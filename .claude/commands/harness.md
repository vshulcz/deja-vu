---
description: Take one harness from "wired" to "the agent is visibly smarter for it"
---

# /harness <name>

`<name>` is one harness. `deja sources` lists every one deja reads and
`deja install` names the ones it can wire on this machine; those two lists are
the authority, not a copy of them here. With no argument, take the harness with
the most sessions in `deja doctor` that has not been through this recently.

## What this is for

deja is not a CLI for a person to run. Its value is that an agent gets smarter
without being asked: it starts a session already knowing what this machine
settled, and it is reminded at the moment it is about to repeat a mistake. A
harness is finished when that happens by itself, looks right on screen, and
costs almost nothing.

Three things are being judged, in this order:

1. **Does the memory reach the model.** Not "the hook is wired" — the bytes.
2. **Is it worth reading.** A count is not a decision. "You have run this
   before" changes nothing; "last time this wiped the staging queue, and the
   flag that fixed it" changes the next tool call.
3. **What it costs.** Latency on every action and tokens in every turn.

## The bar

A harness is done when all of these are true and you have seen each one
yourself, on this machine:

- Every channel the harness offers is wired: session start, per prompt, before
  a tool runs, after one fails, on compaction, and whatever it calls spawning a
  subagent. `deja doctor` reports no row out of date.
- The recall is **in the request the harness actually sent**, read out of the
  bytes, not inferred from the hook printing something.
- A question whose answer exists only in history is answered from history. Use
  an invented token — a real answer cannot be a guess.
- The slash command runs a search. It must not hand the query to deja as its
  first word, and it must survive a query that names one of deja's own flags.
- A second copy of the plugin, or a plugin whose peer is missing, degrades. It
  never takes the profile down.
- The output looks right where the user sees it — the TUI, the toast, the
  collapsed row. Render it and **look at it**.
- The cost is measured and stated: milliseconds per action, bytes per turn.

## Method

Read the code to form a hypothesis. Prove it against something running.

1. **Build a stand.** A scratch config directory for the harness, a scratch deja
   store seeded with an invented decision, and a recording endpoint standing in
   for the model. Never the real index, never the real config: point every
   `DEJA_*_ROOT`/`_DB`/`DEJA_NOTES_FILE` at an empty directory so the machine's
   own sessions cannot leak into a log or a screenshot.
2. **Drive it the way a person would.** Headless run, or the TUI through a
   browser driver. Type the question, wait for the answer to settle.
3. **Read the request.** The recording endpoint holds what the model was sent.
   Grep it for the invented token. That is the only proof that recall arrived.
4. **Look at it.** Screenshot the TUI and open the image. Text assertions miss
   a collapsed row, a truncated line, a block in the wrong place.
5. **Before and after.** Every fix gets both, from the same stand: the old
   build silent, the new one speaking. A fix with no "before" is a guess.
6. **Mutate the test.** Break the fix on purpose and watch the test go red. A
   test that passes on broken code pins nothing — and one that passes by
   skipping pins less.
7. **Measure the cost.** Time the hook on the real store, note the bytes it
   adds. Put both numbers in the PR.

## The budget

The agent must not slow down and its context must not fill up. What the channels
cost today, mean of five runs on a 1,413-session store — treat these as the line
not to cross rather than as a target:

- session start: 117 ms, 3.1 KB, once per session
- per prompt: 132 ms, 1.9 KB when it answers, 0 bytes when it does not
- per tool call: 133 ms, 300–500 bytes when it answers, and it usually does not

Re-measure after a change that touches a hook, and put the number in the PR. A
channel that costs more than this has to earn it with content, not counts.

## Traps that have actually cost time here

- **A reused `session_id` in a probe.** The hooks refuse to repeat themselves
  inside one session, so every probe after the first reads as an empty store.
  Fresh id per probe.
- **A fixture too small.** Rareness is measured against the corpus. In a store
  of three sessions about the same thing, no term identifies anything and the
  prompt hook correctly declines. Seed unrelated background sessions.
- **A fixture that never speaks.** Check the recall is non-empty before
  concluding anything about the harness. A silent stand proves nothing.
- **The wrong build in the stand.** The plugin points at an installed binary;
  the fix is in a fresh one. Check which binary the generated file names.
- **Grepping for one spelling.** "This package has no command" was wrong three
  times: the Rust one spells it `Command::new`, the markdown ones are
  instructions to the model. Open the files.
- **An invented error string.** Shells do not all print what you assume. Run the
  failing command and copy what it actually says.
- **Counting a withheld answer as an answer.** deja distinguishes "nothing
  matched" from "found something, waiting for a second sighting". They are not
  the same result.
- **A test that pins a byte count or a literal parameter list.** It fails on the
  next wording change for a reason unrelated to what it tests.
- **A probe run inside the repository.** Its own output lands in the store and
  becomes tomorrow's history.

## Workflow

Follow the repository's rules: an issue that states the defect, a branch, a PR
whose body carries the measurement, green CI, then merge. Commit messages and PR
bodies in a plain engineer voice — a conventional-commits title and one or two
lines of substance.

Correct yourself in public. If a claim in a merged PR turns out to be wrong,
say so in a comment on it before moving on.

Nothing leaves the machine without being asked: no publishing to npm, no
release tags, no posts, no PRs to other people's repositories.

## What to report

- What now works that did not, with the before/after that proves it.
- The numbers: latency, bytes, hit rate.
- What was measured and found not worth doing — a negative result is a result,
  and it stops the next pass from re-treading it.
- What is left, and what decision it needs.
