# prime-agent (PrimeIntellect)

| Field | Value |
| --- | --- |
| **Format** | JSONL transcript |
| **Default store path** | `~/.prime/agent/sessions/<uuid7>.jsonl` |
| **Env override** | `DEJA_PRIME_ROOT`, `PRIME_AGENT_SESSION_DIR`, `PRIME_AGENT_CODING_AGENT_SESSION_DIR` |
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

## What deja does with it

Sessions are indexed and searchable like any other harness. Wiring — MCP,
hooks, skills, commands, resume, handoff — is not implemented for prime-agent;
`deja install` does not write to its config.

Reported and specified from source by @iMaxTomas in #2529.

**Last verified:** 2026-08-30
