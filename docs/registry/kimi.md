# Kimi Code

- **ID**: `kimi`
- **Store**: `${KIMI_CODE_HOME:-~/.kimi-code}/sessions/<workDirKey>/<sessionId>/agents/main/wire.jsonl`
- **Read override**: `DEJA_KIMI_ROOT` (takes precedence over `KIMI_CODE_HOME` for reads)
- **Format**: append-only JSONL wire protocol (observed `protocol_version` 1.1–1.4)

`state.json` next to each session supplies title, workDir (project) and
timestamps. User turns arrive as `context.append_message`; streamed assistant
turns are reconstructed from `step.begin` → `content.part` (type `text` only;
`think` parts are skipped) → `step.end`, with an end-of-file flush so a
response that is mid-stream when indexing runs is not lost. Sub-agent
histories under `agents/agent-*`, tool payloads and media are out of scope.

- **MCP**: `deja install kimi` writes `mcpServers.deja` into
  `$KIMI_CODE_HOME/mcp.json` (common JSON shape, existing entries preserved).
- **Guidance**: a skill at `$KIMI_CODE_HOME/skills/deja-history/`. Kimi Code scans that directory; the `~/.kimi/skills/` in some write-ups belongs to `MoonshotAI/kimi-cli`, a different tool with the same name. An older deja wrote a block into the global `AGENTS.md`, which install now removes.
- **Resume**: `kimi --session <sessionId>` (verified live on 0.28.1).
- **Handoff**: paste — the CLI has no documented start-with-prompt flag.

Requested and specified by [@yearth](https://github.com/yearth) in
[#248](https://github.com/vshulcz/deja-vu/issues/248).

**Last verified:** 2026-07-28
