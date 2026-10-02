# prime-agent (PrimeIntellect)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.prime/agent/sessions/<uuid7>.jsonl` |
| **Env override** | `DEJA_PRIME_ROOT`, `PRIME_AGENT_SESSION_DIR`, `PRIME_AGENT_CODING_AGENT_SESSION_DIR`; `PRIME_AGENT_CODING_AGENT_DIR` moves the whole `~/.prime/agent` directory (settings, extensions, the default session root) for prime and for deja alike |
| **deja parser** | `internal/sources/prime.go` |
| **Last verified** | 2026-08-30 |

## Discovery

prime-agent (`PrimeIntellect-ai/prime-agent`) keeps one file per session
directly under `~/.prime/agent/sessions/`, named by the session's uuid7. The
root is flat: unlike pi and omp there is no encoded project directory, so the
project comes from the `cwd` the header line carries.

prime-agent relocates the root with two variables of its own,
`PRIME_AGENT_SESSION_DIR` and `PRIME_AGENT_CODING_AGENT_SESSION_DIR`, and deja
reads both — a machine that has moved its sessions has moved them for deja too.
`DEJA_PRIME_ROOT` overrides all of it.

`rlm.spawn` writes each child session as its own transcript beside the root,
under `~/.prime/agent/session-artifacts/<parent-id>/sub-<n>/<child-id>.jsonl`,
with `parentSession` and an `rlmDepth` above 0 in its header. deja reads those
too and files each as a subagent of the parent, read as a Claude Code subagent
is: the task, what it changed and its last turns. `DEJA_INCLUDE_SUBAGENTS=1`
takes the whole run, `=0` leaves children out. The `semantic-edges.jsonl` next
to it is prime's event log, not a transcript.

Older installs kept sessions under `~/.pi/agent/*.jsonl` and in a `--cwd--`
directory beneath this root. prime-agent migrates both into the flat root when
it starts, so the flat layout is what a live install has.

## Shape

The first line is the session header:

```json
{"type":"session","version":3,"id":"<uuid7>","timestamp":"...","cwd":"...","parentSession":null,"rlmDepth":0}
```

The lines after it are typed entries — `message`, `model_change`,
`thinking_level_change`, `service_tier_change`, `compaction`, `branch_summary` —
chained by `parentId`. Message entries carry `content` text blocks.

That is the envelope pi writes, which is why deja reads it with the same parser
pi and omp share: prime-agent descends from the same codebase
(`@earendil-works/pi-coding-agent`) and kept the format.

The model has one tool, `ipython`, so a change to a file is Python in a cell:
the bundled edit skill's `await edit(path, old_str, new_str)`. Each change it
writes lands on the cell's `toolResult` as `details.diffs[{path, oldStr,
newStr, startLine}]`, and deja records the files, the replaced spans and the
written lines from there (#4526). A command run with `bash()` inside a cell is
only in the cell's code and is not recorded as a command.

## What deja does with it

Sessions are indexed and searchable like any other harness.

- **MCP**: `deja install prime` adds a `stdio` entry under `mcpServers` in
  `~/.prime/agent/settings.json` (under `PRIME_AGENT_CODING_AGENT_DIR` when
  that is set).
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`, which
  prime-agent loads.
- **Auto-recall**: `deja install prime-auto` also writes
  `~/.prime/agent/extensions/deja.ts`. At `before_agent_start` it returns the
  session digest on the first turn and per-prompt recall after that;
  `session_start` shows a footer status while the first index builds, and
  `session_compact` runs `deja hook-precompact`. It registers `/deja
  <query>`, which runs `deja search`. `tool_result` does not fire in
  `--print` on 0.9.1, so the repair line after a failed command is not wired.
- **Resume**: `cd <cwd> && prime-agent --resume <id>`, with the `cwd` from the
  header; prime-agent resumes a session only from the project it ran in.
- **Handoff**: exec.

Reported and specified from source by @iMaxTomas in #2529.

**Last verified:** 2026-08-30
