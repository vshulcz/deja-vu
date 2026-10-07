# Kimi Code

- **ID**: `kimi`
- **Store**: `${KIMI_CODE_HOME:-~/.kimi-code}/sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl`
- **Read override**: `DEJA_KIMI_ROOT` (takes precedence over `KIMI_CODE_HOME` for reads)
- **Format**: append-only JSONL wire protocol (observed `protocol_version` 1.1–1.4)

Tool calls are `tool.call` events with `name` and `args`: `path` for the file tools, `old_string`/`new_string` on `Edit`, and the whole file as `content` on `Write`, which is the only record a created file's lines were ever in a session.

`state.json` next to each session supplies title, workDir (project) and
timestamps, and a change to it alone re-reads the session (#4446). User turns arrive as `context.append_message`; streamed assistant
turns are reconstructed from `step.begin` → `content.part` (type `text` only;
`think` parts are skipped) → `step.end`, with an end-of-file flush so a
response that is mid-stream when indexing runs is not lost; the next pass
reads the file whole when the stream goes on, so the reply is stored once
(#4445). `tool.result`
events keep their `output` text under the `tool-output` role, error results
included. Sub-agent histories under `agents/<agent-id>/wire.jsonl` are read
only with `DEJA_INCLUDE_SUBAGENTS=1`, each as a session of its own naming the
parent session (#4483). A `/btw` side question is the exception: Kimi runs it
in a fork of `main` that `state.json` marks `forkedFrom`, and deja reads that
fork by default from the btw reminder on (origin `system_trigger`/`btw` in
0.28, `injection` with variant `btw` from 0.43), so the question and the answer
are kept without the copy of main's context the fork opens with. It is a
session of its own, kind `fork`, naming the session it was asked in. A fork
with no btw reminder, such as an `Agent` call with `fork: true`, is a sub-agent
run like any other (#4484).
Media is out of scope.

- **MCP**: `deja install kimi` writes `mcpServers.deja` into
  `$KIMI_CODE_HOME/mcp.json` (common JSON shape, existing entries preserved).
- **Guidance**: the shared skill at `~/.agents/skills/deja-history/SKILL.md`. Kimi Code 0.28.1 scans both that directory and `$KIMI_CODE_HOME/skills`, and deja writes the shared one so a machine with several harnesses keeps one copy. The `~/.kimi/skills/` in some write-ups belongs to `MoonshotAI/kimi-cli`, a different tool with the same name. An older deja wrote a block into the global `AGENTS.md`, which install now removes.
- **Auto-recall**: `deja install kimi-auto` adds marked `[[hooks]]` entries to
  `$KIMI_CODE_HOME/config.toml`: two `UserPromptSubmit` hooks
  (`deja hook-context --plain --once`, `deja hook-prompt --plain`) and a
  `PreCompact` one, and a `SessionEnd` hook that runs `deja hook-session-end`. Measured on 0.28.1, `UserPromptSubmit` is the only event
  whose output reaches the model, and it takes plain stdout, so the session
  digest rides the first prompt rather than a session-start hook.
- **Resume**: `kimi --session <sessionId>`, run in the `workDir` from the
  session's `state.json`: Kimi refuses a session from any other directory
  (verified live on 0.28.1). A `workDir` that is gone is refused with a
  pointer to `deja show`.
- **Handoff**: exec, `kimi -p`.

Requested and specified by [@yearth](https://github.com/yearth) in
[#248](https://github.com/vshulcz/deja-vu/issues/248).

## Status line

`deja install kimi-auto` adds `[status_line] command` to
`$KIMI_CODE_HOME/tui.toml`. Kimi Code has read it since 0.30.0; 0.28 and 0.29
have no status line and ignore the file. Kimi pipes camelCase JSON with
`sessionId` and no transcript path, and drops an answer slower than 300 ms. A
status line already set there is left alone. Rendered live on 2.1.1.

**Last verified:** 2026-07-28
