# JetBrains AI Assistant

- **ID**: `jetbrains`
- **Store**: `<config>/<Product><version>/workspace/<projectWorkspaceId>.xml`, the chats inside it, and `<config>/<Product><version>/aia-task-history/<chat uid>.events` for agent chats
- **Config root**: `~/Library/Application Support/JetBrains` on macOS, `%APPDATA%\JetBrains` on Windows, `${XDG_CONFIG_HOME:-~/.config}/JetBrains` elsewhere
- **Read overrides**: `DEJA_JETBRAINS_ROOT` replaces the config root
- **Format**: XML for chats; agent task logs are an `AUI_EVENTS_V1` line, then one base64 JSON record per line
- **Needs**: nothing
- **Resume**: none — a chat reopens from the IDE's own history

AI Assistant is the chat inside IntelliJ IDEA, PyCharm, GoLand and the other
JetBrains IDEs. Each product and version has its own config directory
(`IntelliJIdea2026.2`, `PyCharm2026.1`, …), and deja reads all of them.

A project's chats are the `ChatSessionStateTemp` component of its workspace
file. The file is named after the project's workspace id, not its path
(`ProjectStoreImpl` in intellij-community), and `options/recentProjects.xml`
maps it back: `<entry key="$USER_HOME$/src/app">` holds a
`RecentProjectMetaInfo` with that `projectWorkspaceId`. Most workspace files
have no chat; the reader skips those on a byte search.

A chat is a `SerializedChat`: `uid`, `chatModelId`, the title, a start time
under `statisticInformation`, `modifiedAt`, and `messages` — each with
`author` (`Assistant`, or absent for the person), `displayContent` and
`internalContent`.

An agent chat (`chatModelId` starting `agent_`) keeps only the prompts in the
XML. Its work is in `aia-task-history/<uid>.events`: a
`ChatSessionUserPromptEvent` per prompt and a `ChatSessionMessageBlockEvent`
per block, the same blocks Junie's own log has (terminal, file views, file
changes, markdown in `textChunk` pieces, result). They are read as commands,
files, edits and text.

When `<uid>.agentsession` names an agent whose own store deja reads —
`acp.registry.junie`, `claude-acp`, `codex-acp`, `github-copilot`, `cline`,
`kilo`, `opencode` — that store has the whole conversation, and the chat is
left to it rather than read twice.

**Last verified:** 2026-10-08

## Known quirks and drift

- **No per-message time.** The XML stores the chat's start and last change,
  nothing per message, and the task log nothing per record. Messages take the
  start a millisecond apart, so two identical turns stay two.
- **Checked against** chats and task logs from AI Assistant 2025.1 to 2026.2
  in public dotfiles, and the 262.10968.170 plugin's classes. No signed-in IDE
  ran on a stand.
- **Wiring.** `deja install jetbrains` writes the server into
  `~/.ai/mcp/mcp.json` under `mcpServers`, the global MCP file every JetBrains
  IDE's AI Assistant reads (`McpApplicationServerConfigurationService`).
- **No hooks.** Nothing in the 262.10968.170 plugin runs a user hook, so
  recall reaches the chat as the MCP tool only: no digest, per-prompt, tool,
  failure, compaction or session-end surface.
- **Rules are per project.** AI Assistant reads rules from a project's
  `.aiassistant/rules/*.md` and custom instructions from a project setting;
  there is no user-level file for `deja rules` to write.
- **Junie in the IDE.** The Junie agent AI Assistant runs keeps its sessions
  in Junie's own store, read as `junie`.
