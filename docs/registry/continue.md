# Continue

- **ID**: `continue`
- **Store**: `${CONTINUE_GLOBAL_DIR:-~/.continue}/sessions/<sessionId>.json`, one document per session, with `sessions.json` beside them as the list.
- **Read override**: `DEJA_CONTINUE_ROOT`
- **Format**: whole-file JSON rewritten on change; full re-parse per pass.

Continue runs in VS Code and JetBrains, and its chat, agent and plan modes all
write the same file. The session document holds `history[]`, each item a
`message` with a `role` and a `content` that is either a string or an array of
`{type, text}` parts; an assistant item that called tools carries them in
`toolCallStates[]`. `system` and `tool` roles are skipped — the first is
configuration, and a tool's result arrives under the assistant item that asked
for it. The tool calls themselves are not indexed as work records: they are
model tools (`read_file`, `edit_file`) rather than commands anyone ran, and the
command index is for the latter.

Nothing in the file carries a timestamp. `sessions.json` records `dateCreated`
and `workspaceDirectory` per session, so that date is the session's start and
the file's mtime is its update; turns are laid out in order from the start,
which is enough to order them within the session. The project name comes from
`workspaceDirectory`, falling back to the list entry when the document omits it.

Shape verified against Continue's own types (`core/index.d.ts`: `Session`,
`ChatHistoryItem`, `ChatMessage`) and `core/util/paths.ts`; a live-store
validation is still welcome.

- **MCP**: `deja install continue` adds the server to `mcpServers:` in
  `~/.continue/config.yaml` — a list of mappings, not the keyed object other
  harnesses use, so the entry is found again by its `name`. Verified on
  @continuedev/cli 1.5.47 against a recording endpoint: the `deja` tool is in
  the tool list of every request, and a call came back with the seeded decision
  in the bytes Continue sent next. The TUI header lists it under `MCP Servers`.
- **Skill**: the same install writes `~/.continue/skills/deja-history/SKILL.md`.
  Continue reads skills from its global folder and from `<workspace>/.claude/skills`,
  and names each one in the `Skills` tool's own description — so it is in front
  of the model on every turn, which is what makes recall arrive unasked.
- **Command**: `prompts:` in the same config carries `/deja`. Continue expands
  slash commands in its TUI; in `-p` headless mode the text goes through
  verbatim, so the entry is written but its expansion is unverified here.
- **Auto-recall**: none yet. The CLI carries a Claude-shaped hooks system —
  Claude's own event set, `additionalContext`, settings read from
  `~/.continue/settings.json` — and nothing fires it in 1.5.47: hooks written
  for every event loaded (its own log says "Hooks loaded: 8 handler(s) across 8
  event type(s)") and no handler ran, while the same run's tool call went
  through. It becomes work the day a release fires them.
- **Resume**: `cn --resume` reopens the last session and `--fork <id>` branches
  from one, but nothing takes the id of an arbitrary session; the editor
  reopens one from its history view.
- **Handoff**: paste.

Costs on that version, measured from the recorded requests: deja's tool schema
is 3,112 bytes of the 10 KB tool block in every request, the skill adds 700
bytes to the `Skills` tool's description, and a recall that answers adds 1,384
bytes to the turn that asked.

**Last verified:** 2026-09-07
