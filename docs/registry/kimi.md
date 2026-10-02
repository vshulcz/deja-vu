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
included. Sub-agent histories under `agents/agent-*` and media are out of
scope.

- **MCP**: `deja install kimi` writes `mcpServers.deja` into
  `$KIMI_CODE_HOME/mcp.json` (common JSON shape, existing entries preserved).
- **Guidance**: the shared skill at `~/.agents/skills/deja-history/SKILL.md`. Kimi Code 0.28.1 scans both that directory and `$KIMI_CODE_HOME/skills`, and deja writes the shared one so a machine with several harnesses keeps one copy. The `~/.kimi/skills/` in some write-ups belongs to `MoonshotAI/kimi-cli`, a different tool with the same name. An older deja wrote a block into the global `AGENTS.md`, which install now removes.
- **Auto-recall**: `deja install kimi-auto` adds marked `[[hooks]]` entries to
  `$KIMI_CODE_HOME/config.toml`: two `UserPromptSubmit` hooks
  (`deja hook-context --plain --once`, `deja hook-prompt --plain`) and a
  `PreCompact` one. Measured on 0.28.1, `UserPromptSubmit` is the only event
  whose output reaches the model, and it takes plain stdout, so the session
  digest rides the first prompt rather than a session-start hook.
- **Resume**: `kimi --session <sessionId>`, run in the `workDir` from the
  session's `state.json`: Kimi refuses a session from any other directory
  (verified live on 0.28.1). A `workDir` that is gone is refused with a
  pointer to `deja show`.
- **Handoff**: exec, `kimi -p`.

Requested and specified by [@yearth](https://github.com/yearth) in
[#248](https://github.com/vshulcz/deja-vu/issues/248).

**Last verified:** 2026-07-28
