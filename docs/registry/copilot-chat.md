# VS Code Copilot Chat

- **ID**: `copilot-chat`
- **Store**: two, and a newer extension writes only the second. VS Code User folder `workspaceStorage/<hash>/chatSessions/<sessionId>.jsonl` (flat `.json` on older builds), empty-window chats under `globalStorage/emptyWindowChatSessions/`, and the extension's own `workspaceStorage/<hash>/GitHub.copilot-chat/transcripts/<sessionId>.jsonl`. Code, Code Insiders and VSCodium hosts. Not Copilot CLI (`copilot`).
- **Read override**: `DEJA_COPILOT_CHAT_ROOTS` (path list of User folders)
- **Format**: two, one per store. The `chatSessions` file is a JSONL mutation log (`kind` 0 initial / 1 set / 2 push / 3 delete) or whole-file JSON; full re-parse per pass, because compaction rewrites the file and a byte-offset resume would apply deltas to state it never saw. The `GitHub.copilot-chat/transcripts` file is a `type`-discriminated event log — `session.start`, `user.message`, `assistant.message`, `assistant.turn_start`/`turn_end`, `tool.execution_start`/`complete` — read by `internal/sources/copilot_agent.go`.

On VS Code Server 1.137 there is no `chatSessions` directory at all: the machine in #3637 had 47 transcripts holding eleven weeks of chats and deja found zero files. On desktop 1.136.1 both layouts sit side by side — 28 `chatSessions` directories and five transcripts — so the second store is where a chat written by the newer extension goes, whatever the host. A transcript with no `user.message` in it is an agent run and is indexed: 11 of those 47 and all five here have none, and what the agent said and the files it touched are the only record of that work.

`globalStorage/github.copilot-chat/session-store.db` is a third store and is not read: every id in it also exists as a transcript, so it adds the session's `cwd` rather than history, and on the reporter's machine it covered the last two days against the transcripts' eleven weeks.

`workspace.json` beside the storage directory holds `{"folder":"file:///…"}` (or `workspace` for a multi-root `.code-workspace`); the project name is the last path segment. Empty-window sessions take `workingDirectory` the same way, or `-`. `inputState` is not read (it carries the GitHub account label).

User turns are `message` as a string or `{text}`. Assistant speech is bare `{value}` markdown chunks (and a plain string in old files); `thinking` and UI chrome (`progressMessage`, `warning`, `info`, `systemNotification`) are skipped. Tool paths come from `toolInvocationSerialized.resultDetails` and `inlineReference`; terminal commands from `toolSpecificData.commandLine`.

Edits are `textEditGroup` parts: a `uri` and a list of lists of `{text, range}`, where the text is what replaced the range. Only the written side is there — the range says where the old text was, not what it said — so the file and hashes of the written lines are recorded and no replaced span is. Counted on one machine's store: 655 edit groups across 34 session files, which became 624 written records over 6 sessions and 23,141 hashed lines.

- **MCP**: `deja install vscode` writes the user `mcp.json` VS Code reads in agent mode (top-level `servers`, `type: stdio`). Verified on VS Code 1.134.0: Copilot Chat started the server and called the `deja` tool with `mode: recall`.
- **Skill**: the same install writes `<User>/prompts/deja.instructions.md` with `applyTo: "**"`, VS Code's custom instructions, which are in front of the model on every chat. Verified on 1.134.0: with that file and no mention of deja in the question, Copilot Chat called `recall` on its own.
- **Command**: a prompt file. `deja install vscode` writes
  `<User>/prompts/deja.prompt.md` for every host it finds — the same `User`
  directory this reader walks for workspaceStorage — and the chat box lists it
  as `/deja`. The frontmatter carries the description shown beside it and an
  argument hint, and the body tells the model to call the recall tool, with the
  CLI as the fallback when the tool is not in that window. This is the only way
  in here: Copilot Chat fires no session-start or per-prompt hook, so nothing
  arrives unasked.
- **Auto-recall**: none in the hook sense — Copilot Chat has no session-start or per-prompt event. The instructions file is what makes recall arrive without being asked for.
- **Resume**: Chat: Show Chats… in the editor, not a command.
- **Handoff**: paste.

**Last verified:** 2026-09-20
