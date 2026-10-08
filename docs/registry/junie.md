# Junie

- **ID**: `junie`
- **Store**: `${JUNIE_HOME:-~/.junie}/sessions/<session-id>/events.jsonl` — one event log per session, listed in `sessions/index.jsonl`
- **Read overrides**: `DEJA_JUNIE_ROOT` replaces the sessions directory; `JUNIE_HOME` moves the whole store
- **Format**: JSONL — one event per line, `{kind, timestampMs, …}`
- **Needs**: nothing
- **Resume**: `cd <projectDir> && junie --session-id <id> --resume`, the project from `sessions/index.jsonl`

Junie is JetBrains' coding agent: the `junie` CLI, and the agent AI Assistant
runs in the IDE. Both keep their sessions here. The home is `$JUNIE_HOME`, else
`.junie` under the JVM's `user.home` — not `$HOME`.

A session directory holds `events.jsonl`, `state.json` (the agent's last
state), `transcript.md` (a rendering of the same log) and one `task-*`
directory per task. `index.jsonl` has a line per session with its
`projectDir` and `taskName`.

In the log, `UserPromptEvent` carries what the person typed (`prompt`,
`presentablePrompt`). The agent's work arrives as
`SessionA2uxEvent{event:{state, agentEvent:{kind, stepId, …}}}`, one block per
step, updated in place:

- `TerminalBlockUpdatedEvent` — `command`, `status` (`FAILED` on a non-zero
  exit), `exitCode`, `output`. Read as a command, `→ exit N` when it failed,
  and its output.
- `ViewFilesBlockUpdatedEvent` — `files[].relativePath`, which holds an
  absolute path. Read as files.
- `FileChangesBlockUpdatedEvent` — `changes[]` with `beforeContent.text`,
  `afterContent.text` and the paths. Read as an edit of the changed lines and
  the written side.
- `MarkdownBlockUpdatedEvent` and `ResultBlockUpdatedEvent` — the agent's
  text and the task's result.
- `ContextCompactionBlockUpdatedEvent` — a compaction, written when the next
  task starts.

Blocks are folded by `stepId`, the last update winning. A finished task
replays all of its blocks with the event's state `COMPLETED`; those fold into
the step they repeat. A headless run (`junie "task"`) writes no
`UserPromptEvent`: its task is read from the issue description in
`state.json`, with the `<additional_context>` hooks added in front of it cut.

**Last verified:** 2026-10-08

## Known quirks and drift

- **Checked against a running install.** Junie CLI 3110.7 against a stub
  endpoint, with a custom model profile and no account, in an isolated
  `JUNIE_HOME`.
- **Wiring.** `deja install junie` writes the server into
  `$JUNIE_HOME/mcp/mcp.json` under `mcpServers`. Junie asks the model which
  capabilities a prompt needs before the task runs, and offers the tool as
  `mcp_deja_deja` only when that pass picks it. The `/deja` command goes into
  `$JUNIE_HOME/commands/deja.md`. The skill goes in `~/.agents/skills`, which
  Junie reads beside its own `skills/`. Rules go in `$JUNIE_HOME/AGENTS.md`,
  which is in front of every task. A project's `.junie/` files are read only
  once the project is trusted.
- **Auto-recall.** `deja install junie-auto` adds hooks to
  `$JUNIE_HOME/config.json`, in Claude Code's schema. Junie puts a hook's
  stdout, plain or as `hookSpecificOutput.additionalContext`, in
  `<additional_context>` before the prompt or the tool's result:
  - `UserPromptSubmit` runs `hook-context --once` (the digest, first prompt
    only) and `hook-prompt`. SessionStart's output is not delivered.
  - `PreToolUse` on `Bash|Read|Edit` runs `hook-tool`. It also fires for
    Junie's own tools, such as `submit`, so the matcher is narrow.
  - `SessionEnd` ends the live stamp.
  - `Stop` is not wired: any output there keeps the task from finishing.

  The payload's `cwd` is Junie's home, not the project; `--junie` takes the
  project from `project_path`.
- **No PostToolUse, no PreCompact.** 3110.7's hook events are SessionStart,
  UserPromptSubmit, PreToolUse, PermissionRequest, Stop, StopFailure and
  SessionEnd. The `--junie` hooks read the rest from `events.jsonl`: when the
  newest command failed, its fix pair rides the next PreToolUse or prompt; a
  compaction is captured there too, and its packet delivered.
- **Status line.** None to write to: the toolbar takes only built-in items
  (`ToolbarElement`).
- **Agent chats in the IDE.** AI Assistant runs Junie over ACP and points its
  chat at the Junie session (`aia-task-history/<chat>.agentsession`); the
  `jetbrains` reader leaves such a chat to this one.
