# VS Code Copilot Chat

- **ID**: `copilot-chat`
- **Store**: two, and a newer extension writes only the second. VS Code User folder `workspaceStorage/<hash>/chatSessions/<sessionId>.jsonl` (flat `.json` on older builds), empty-window chats under `globalStorage/emptyWindowChatSessions/`, and the extension's own `workspaceStorage/<hash>/GitHub.copilot-chat/transcripts/<sessionId>.jsonl`. A named profile keeps its own copy of both under `profiles/<id>/`, and those are read too. Code, Code Insiders and VSCodium hosts. Not Copilot CLI (`copilot`).
- **Read override**: `DEJA_COPILOT_CHAT_ROOTS` (path list of User folders)
- **Format**: two, one per store. The `chatSessions` file is a JSONL mutation log (`kind` 0 initial / 1 set / 2 push / 3 delete) or whole-file JSON; full re-parse per pass, because compaction rewrites the file and a byte-offset resume would apply deltas to state it never saw. The `GitHub.copilot-chat/transcripts` file is a `type`-discriminated event log — `session.start`, `user.message`, `assistant.message`, `assistant.turn_start`/`turn_end`, `tool.execution_start`/`complete` — read by `internal/sources/copilot_agent.go`.

On VS Code Server 1.137 there is no `chatSessions` directory at all: the machine in #3637 had 47 transcripts holding eleven weeks of chats and deja found zero files. On desktop 1.136.1 both layouts sit side by side — 28 `chatSessions` directories and five transcripts — so the second store is where a chat written by the newer extension goes, whatever the host. A transcript with no `user.message` in it is an agent run and is indexed: 11 of those 47 and all five here have none, and what the agent said and the files it touched are the only record of that work.

`globalStorage/github.copilot-chat/session-store.db` is a third store and is not read: every id in it also exists as a transcript, so it adds the session's `cwd` rather than history, and on the reporter's machine it covered the last two days against the transcripts' eleven weeks.

`workspace.json` beside the storage directory holds `{"folder":"file:///…"}` (or `workspace` for a multi-root `.code-workspace`); the project name is the folder's last two path segments, escapes decoded, as Claude Code names a directory (#4457). A folder on a UNC share keeps its host, so resume opens `\\server\share\…` (#4462). Empty-window sessions take `workingDirectory` the same way, or `-`. `inputState` is not read (it carries the GitHub account label).

User turns are `message` as a string or `{text}`. Assistant speech is bare `{value}` markdown chunks (and a plain string in old files); `thinking` and UI chrome (`progressMessage`, `warning`, `info`, `systemNotification`) are skipped. An `inlineReference` part is read into the reply as the name VS Code draws for it: the part's `name`, a symbol's name, or the file's base name (#4589). Agent mode writes an edit as an empty code fence around a `codeblockUri` and the `textEditGroup`; deja reads that as the file's name on its own line, where VS Code draws a pill naming it (#4590). Tool paths come from `toolInvocationSerialized.resultDetails` and `inlineReference`, and for `copilot_readFile`, which has no `resultDetails`, from the `uris` of its `pastTenseMessage` or `invocationMessage` (the other tools' uris there are directories, workspace-wide problem lists or the extension's own files); terminal commands from `toolSpecificData.commandLine`.

Edits are `textEditGroup` parts: a `uri` and a list of lists of `{text, range}`, where the text is what replaced the range. Only the written side is there — the range says where the old text was, not what it said — so the file and hashes of the written lines are recorded from it. Counted on one machine's store: 655 edit groups across 34 session files, which became 624 written records over 6 sessions and 23,141 hashed lines.

The replaced side is in the edit call itself. Agent mode stores each request's calls under `result.metadata.toolCallRounds[].toolCalls[]` as `{name, arguments, id}`, the arguments a JSON string, and each call's result under `toolCallResults[id]`: `replace_string_in_file` `{filePath, oldString, newString}`, `multi_replace_string_in_file` `{replacements: [{filePath, oldString, newString}]}`, `create_file` `{filePath, content}` and `apply_patch` `{input}` in codex's patch format. deja records the replaced span of each, and skips a call whose result opens with `ERROR` or `Applying patch failed`. The transcripts carry the same calls under `tool.execution_start`, with `success` on `tool.execution_complete`; there both sides come from the call, since no `textEditGroup` is written. On the same store: 352 `replace_string_in_file`, 190 `apply_patch`, 19 `multi_replace_string_in_file` and 118 `create_file` calls, which became 638 replaced spans over 6 sessions (#595).

- **MCP**: `deja install vscode` writes the user `mcp.json` VS Code reads in agent mode (top-level `servers`, `type: stdio`). Verified on VS Code 1.134.0: Copilot Chat started the server and called the `deja` tool with `mode: recall`.
- **Skill**: the same install writes `<User>/prompts/deja.instructions.md` with `applyTo: "**"`, VS Code's custom instructions, which are in front of the model on every chat. Verified on 1.134.0: with that file and no mention of deja in the question, Copilot Chat called `recall` on its own.
- **Command**: a prompt file. `deja install vscode` writes
  `<User>/prompts/deja.prompt.md` for every host it finds — the same `User`
  directory this reader walks for workspaceStorage — and the chat box lists it
  as `/deja`. The frontmatter carries the description shown beside it and an
  argument hint, and the body tells the model to call the recall tool, with the
  CLI as the fallback when the tool is not in that window.
- **Auto-recall**: `deja install vscode-auto` (or `copilot-auto`; both write the
  same file) puts deja's hooks in `~/.copilot/hooks/deja.json`. VS Code 1.140
  lists that directory among its hook locations, `chat.useHooks` is on by
  default, and Copilot Chat 0.68 runs `SessionStart`, `UserPromptSubmit`,
  `PreToolUse`, `PostToolUse` and `PreCompact`. It maps the camelCase events
  it shares with Copilot CLI (`sessionStart`, `userPromptSubmitted`,
  `preToolUse`, `postToolUse`) and reads `PreCompact` under that name, so one
  file serves both hosts. The payload carries `hook_event_name`, `session_id`
  and `transcript_path` (the `GitHub.copilot-chat/transcripts` file above), and
  deja answers in the nested `hookSpecificOutput` shape, the only one VS Code
  reads for `SessionStart`, `PreToolUse` and `PostToolUse`. VS Code drops
  `matcher`, so the pre-tool hook runs for every tool and speaks for
  `replace_string_in_file`, `create_file`, `insert_edit_into_file` and
  `run_in_terminal`; a failed terminal command reaches `PostToolUse` with
  `Command exited with code N` in the result text. `PreCompact` output is
  ignored by VS Code; deja captures the transcript there and hands the packet
  back on the next prompt or tool call. Copilot Chat never runs a
  `SessionEnd` hook (no call site in 0.68), so the live stamp expires on its
  own. Checked against the workbench and extension source; not yet run
  against a live VS Code.
- **Resume**: Chat: Show Chats… in the editor, not a command. The list holds only the open workspace’s chats; `deja resume <id>` names the folder to open first.
- **Handoff**: exec, `code chat -m agent <prompt>` opens VS Code's chat in agent mode with it.

**Last verified:** 2026-10-07
