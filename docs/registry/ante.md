# Ante session format

## Store and files

Ante (`AntigmaLabs/ante`) stores sessions under `${ANTE_HOME:-~/.ante}/sessions/<session-id>/`
with `<session-id>` shaped as a `ses_<ULID>` directory name. `DEJA_ANTE_ROOT` replaces the
`sessions/` directory directly, and `DEJA_ANTE_ROOTS` adds extra roots. Each session
directory contains:

- `events.jsonl`: append-only event stream written during live runs.
- `meta.json`: session metadata (`id`, `provider`, `model`, `dir` / `cwd`,
  `first_user_message`, `usage`, `started_time`, `last_active_time`, `forked_from`).
- `dialog.json`: optional full-conversation snapshot. When `events.jsonl` exists beside
  it, `events.jsonl` is parsed and `dialog.json` is treated as a sidecar so `deja doctor`
  does not count it twice; when `events.jsonl` is absent, `parseAnteDialog` reads `dialog.json`.
- `subagents/<subagent-id>/`: nested subagent session directories (`Kind: subagent`,
  unless `DEJA_INCLUDE_SUBAGENTS=0`).

## Records

Each line of `events.jsonl` is a JSON envelope with `timestamp`, `id`, optional `parent`,
and a tagged `event` object:

```json
{"timestamp":"2026-08-14T10:00:05Z","id":"evt_02","event":{"UserInput":"Fix the retry timeout in backoff.go"}}
```

- `SessionStart`: fills missing `id`, `forked_from`, `provider`/`model`, and `dir`.
- `UserInput`: `user` message; the first line seeds the session title when `meta.json` omits it.
- `AgentMessage`: `assistant` message.
- `CompactEnd`: `summary` record from `summary`.
- `ShellOutput`: bash command (`command`, `exit_code`) and optional tool output.
- `ToolStart` / `ToolEnd`: `tool_use` / completion status (`Success`, `Failed`, `Cancelled`,
  `Denied`) mapped through the Claude tool dialect for file paths, edit spans, commands,
  and exit codes.
- `UsageUpdate` / `SessionEnd`: token usage counters (falling back to `meta.usage`).

`dialog.json` snapshots store `reasoning_model` and a `messages` array (`role`: `user`
or `model`/`assistant`) with `content.Text` or `content.Parts[]` (`Text`, `ToolUse`, `ToolResult`).

## Wiring and resume

- **MCP (`deja install ante`)**: merges `mcp_servers.deja` (`command` + `args`) into
  `${ANTE_HOME:-~/.ante}/settings.json`, matching Ante's `mcp.mdx` schema.
- **Skills & slash commands**: writes `~/.agents/skills/deja-history/SKILL.md`. Ante
  discovers skills under `~/.agents/skills/` and invokes them as `/<skill-name> [args]`.
- **Rules (`deja rules sync`)**: syncs into `${ANTE_HOME:-~/.ante}/AGENTS.md` (Ante also
  falls back to `~/.claude/CLAUDE.md` when `~/.ante/AGENTS.md` is absent).
- **Hooks & status line**: `AntigmaLabs/ante` v0.3.7 exposes no user-configurable
  lifecycle hooks (`auto: false`), and `status_line_command` in `~/.ante/settings.json`
  replaces Ante's built-in status bar with no `stack_with_default` flag, so `deja install ante`
  does not overwrite it.
- **Resume & handoff**: `deja resume` runs `ante --resume <id>` in the recorded working
  directory; `deja handoff --to ante --exec` runs `ante -p <packet>`. `WriteBack` refuses
  because `events.jsonl` and `dialog.json` belong to an external active format.

## Synthetic fixture

[`fixtures/registry/ante/sessions/ses_01M7WQZKYBJ61WM9H6TR6FP4CY/events.jsonl`](../../fixtures/registry/ante/sessions/ses_01M7WQZKYBJ61WM9H6TR6FP4CY/events.jsonl)
and sibling `meta.json` form the synthetic conformance sample with deterministic IDs and timestamps.

**Last verified:** 2026-11-20 against AntigmaLabs/ante v0.3.7
