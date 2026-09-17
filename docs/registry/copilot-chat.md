# VS Code Copilot Chat

- **ID**: `copilot-chat`
- **Store**: VS Code User folder `workspaceStorage/<hash>/chatSessions/<sessionId>.jsonl` (flat `.json` on older builds); empty-window chats under `globalStorage/emptyWindowChatSessions/`. Code, Code Insiders and VSCodium hosts. Not Copilot CLI (`copilot`).
- **Read override**: `DEJA_COPILOT_CHAT_ROOTS` (path list of User folders)
- **Format**: JSONL mutation log (`kind` 0 initial / 1 set / 2 push / 3 delete) or whole-file JSON; full re-parse per pass. Compaction rewrites the file, so a byte-offset resume would apply deltas to state it never saw.

`workspace.json` next to `chatSessions` holds `{"folder":"file:///…"}` (or `workspace` for a multi-root `.code-workspace`); the project name is the last path segment. Empty-window sessions take `workingDirectory` the same way, or `-`. `inputState` is not read (it carries the GitHub account label).

User turns are `message` as a string or `{text}`. Assistant speech is bare `{value}` markdown chunks (and a plain string in old files); `thinking` and UI chrome (`progressMessage`, `warning`, `info`, `systemNotification`) are skipped. Tool paths come from `toolInvocationSerialized.resultDetails` and `inlineReference`; terminal commands from `toolSpecificData.commandLine`.

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

**Last verified:** 2026-09-06
