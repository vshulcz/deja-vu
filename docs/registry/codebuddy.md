# CodeBuddy Code

- **ID**: `codebuddy`
- **Store**: `${CODEBUDDY_CONFIG_DIR:-~/.codebuddy}/projects/<mangled-cwd>/<session-id>.jsonl` — one file per session
- **Sub-agents**: `<mangled-cwd>/<session-id>/subagents/agent-<id>.jsonl`, read as their task, edits and last turns; `DEJA_INCLUDE_SUBAGENTS=1` takes the whole run, `=0` skips them
- **WorkBuddy**: `~/.workbuddy/projects/...` (workbuddy.cn) and `~/.workbuddy-ai/projects/...` (WorkBuddy AI, workbuddy.ai), or `$WORKBUDDY_CONFIG_DIR/projects`; the same layout and records, read when they exist
- **IDE 4.x**: `CodeBuddyExtension/Data/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/<conversation-id>/index.json` under `~/Library/Application Support` (macOS), `~/AppData/Local` (Windows) or `~/.local/share` (Linux) — one directory per conversation, read as the same `codebuddy` harness ([below](#codebuddy-ide-4-x))
- **Read overrides**: `DEJA_CODEBUDDY_ROOTS`, a path list, replaces both CLI stores; `DEJA_CODEBUDDY_LEGACY_ROOTS`, a path list, replaces the IDE 4.x root
- **Format**: JSONL — OpenAI Responses-style items; IDE 4.x keeps one JSON file per message
- **Needs**: nothing
- **Wiring**: `deja install codebuddy` adds the MCP server; `deja install codebuddy-auto` adds the hooks as well. `workbuddy` and `workbuddy-auto` do the same in the WorkBuddy home

CodeBuddy Code is Tencent's terminal agent (`@tencent-ai/codebuddy-code`). Its
store is laid out like Claude Code's — a directory per working directory, one
JSONL per session — but the directory name is the cwd with `/`, `\` and `:`
turned into `-` and the leading dash dropped, and the records inside are not
Claude Code's. Each line is one item:

```
{"type":"message","role":"user","content":[{"type":"input_text","text":"..."}],"timestamp":1780000000000,"cwd":"/repo","providerData":{...}}
{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"..."}]}
{"type":"function_call","callId":"call_1","name":"Bash","arguments":"{\"command\":\"go test ./...\"}"}
{"type":"function_call_result","callId":"call_1","name":"Bash","output":{"type":"text","text":"..."}}
```

plus `reasoning`, `summary` (the compaction digest), `ai-title`,
`custom-title`, `topic`, `turn-metrics`, `file-history-snapshot` and
`session-meta`. Timestamps are epoch milliseconds and `cwd` is on the record.
The tools are Claude Code's by name and arguments (`Bash`, `Read`, `Edit`,
`Write`, `MultiEdit`, `file_path`, `old_string`), so a call becomes a command,
file, edit or wrote record the way it does for Claude Code, and a
`function_call_result` becomes tool output.

Role `user` is shared with the client's own plumbing. A record with
`providerData.skipRun` never reached the model as anyone's words (hook output,
filter notices, local commands), and `isMeta`, `isCompactInternal`,
`isCompacted`, `isSummary`, `compactType` and `teammateMessage` mark
continuation, compaction and teammate turns. All are dropped, which is what
CodeBuddy's own `isRealUserMessageItem` does. A text item that opens with
`<command-name>`, `<system-reminder>`, `<local-command-stdout>`,
`<teammate-message>` or a task notification is dropped from the turn it rides in.
`reasoning` and `summary` are not indexed.

The title follows CodeBuddy's own order: the newest `custom-title`, then the
newest `ai-title`, then the newest `topic`, skipping the placeholders it skips.

MCP servers go into the first of `<config>/.mcp.json`, `<config>/mcp.json` and
`~/.codebuddy.json` that exists, else `<config>/.mcp.json` — the one file
CodeBuddy reads for user scope. Hooks go into `<config>/settings.json` in the
Claude Code shape, `timeout` in seconds: `SessionStart`, `UserPromptSubmit`,
`PostToolUse` and `PostToolUseFailure` on `Bash|PowerShell`, `PostToolUse` on
`Read|Edit|Write|MultiEdit` (the line deja keeps for that file), `PreCompact`
and `SessionEnd`. CodeBuddy reads `hookSpecificOutput.additionalContext` from all
of them the way Claude Code does.

WorkBuddy is a desktop app that runs this agent with `CODEBUDDY_CONFIG_DIR`
set to its own home, so `~/.codebuddy` wiring never reaches it.
`deja install workbuddy-auto` writes the same hooks into that home:
`$WORKBUDDY_CONFIG_DIR`, else whichever of `~/.workbuddy` and
`~/.workbuddy-ai` exists. A new MCP file there is `mcp.json`, the one the
app's own server settings write, since the agent reads only the first of
`.mcp.json` and `mcp.json`. WorkBuddy AI keeps a server from that file off
until you click Trust for it in Settings > MCP and restart the app; install
says so, and `deja doctor` reads the row as `untrusted` until then.

**Last verified:** 2026-11-25

## Status line

`deja install codebuddy-auto` sets `statusLine.command` in
`~/.codebuddy/settings.json`. CodeBuddy pipes Claude's payload; before the
first prompt the session id is `unknown` and there is no transcript path, and
deja shows the day's line alone. A status line already set there is left
alone. Rendered live on 2.16.0. WorkBuddy is not wired: its status line is
unchecked.

## Known quirks and drift

- **Checked against a live 2.161.2.** Sessions written by the CLI itself
  (pointed at a local model through `models.json`) match the record shapes,
  the store path and the hook payloads read out of `dist/codebuddy.js`; the
  fixture is synthetic.
- **IDE 5.x writes this store.** CodeBuddy IDE 5.x runs CodeBuddy Code
  underneath and keeps its conversations here, so they are read as CLI
  sessions. IDE 4.x kept its own store, read as described below.
- **Exit codes from the result text.** A shell result ends with
  `Exit Code: N` (`(none)` when a signal ended it), and `status` stays
  `completed` either way, so the code is read off that line.
- **`/compact` writes its instruction prompt as a user record** marked only
  `providerData.agent: "compact"`; it is skipped, as the summary after it is.
- **Resume needs the session's directory.** `codebuddy -r <id>` finds a
  session only under the folder of the directory it is run from.
- **`-r` alone drops the SessionStart context.** CodeBuddy hands it to the
  model on a resume only with `-c` set, and the `-r` id still picks the
  session, so `deja resume` prints `codebuddy -c -r <id>`.
- **WorkBuddy shares the harness id.** Its sessions read as `codebuddy`.
- **WorkBuddy checked against 5.6.2.** The agent bundled in WorkBuddy AI,
  run headless with the env the app spawns it with, writes the store above
  and runs the `settings.json` hooks and the `mcp.json` server. The
  workbuddy.cn build names its home `.workbuddy` in the same `product.json`.
  `deja resume` has no command for WorkBuddy yet.

## CodeBuddy IDE 4.x

Before 5.x, the IDE kept its chats in the extension's data directory, which
the 5.x IDE still resolves the same way (`resolveExtensionUserDataPath` in its
genie extension): `CodeBuddyExtension/Data` under
`~/Library/Application Support` on macOS, `~/AppData/Local` on Windows and
`~/.local/share` elsewhere. On Windows a
`~/AppData/Roaming/CodeBuddyExtension/Data` is read too when it is there.

```
<Data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/index.json
<Data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/<conversation-id>/index.json
<Data>/<user>/CodeBuddyIDE/<user>/history/<workspace-md5>/<conversation-id>/messages/<message-id>.json
```

A signed-out IDE files under the user `default`, and an older one put
`history/` one level higher, so a conversation is any `<id>/index.json` two
levels under a directory named `history`. `DEJA_CODEBUDDY_LEGACY_ROOTS` takes
a `Data` directory, one user's directory, a `history` directory, one
workspace's directory, or a copy of any of them from another machine.

The workspace's `index.json` lists its conversations with a title, a type and
times. A conversation's `index.json` lists its messages and its requests; each
request names the messages it produced, its start time and token usage. Each
message file holds `role`, a `message` and an `extra` that are JSON objects
written as strings, and usually `createdAt`. A conversation with no
`createdAt` anywhere takes each message's time from its request, and one with
no time at all, in its messages, its requests or the workspace index, takes
the modification time of its `index.json`.

The IDE can keep one conversation in two workspace directories, file for file
under the same conversation id. It is read once: from the copy whose
`index.json` was written last, or from the one whose path sorts first when
both were written at once. If that copy is deleted, the other takes over.

The reading follows the 5.x IDE's own converter (`convertCraftConversation`),
so a conversation reads the same before and after the upgrade:

- **User turns** are what the person typed: the input box's blocks when they
  hold a file mention or an image (as `@path`), else the `<user_query>` inside
  the `<user_info>`, `<additional_data>` and `<system_reminder>` envelope, else
  the recorded input phrase. The workspace folder named in `<user_info>` is the
  session's project. A turn that is only envelope — the reminder the IDE sends
  after a crash — is dropped.
- **Answers to the agent's questions** (`ask_followup_question`) come back as
  XML in the prompt and as `extra.questionAnswer`; each is indexed as
  `question → answer`.
- **Prompts that mention a file or an image** read the way the converter writes
  them: `@/path/to/file`, `@image#1:name.png`, `/command`. A code selection
  attached to a prompt is context, not what was typed, and is left out.
- **Messages between team members** (`<teammate-message>`) are read into the
  team member's own conversation, as `**sender**` and the message, and left out
  of the lead's.
- **Assistant text** is indexed. `reasoning` is not, as for CodeBuddy Code.
  Images are not indexed.
- **Tool calls**: `read_file`, `write_to_file`, `replace_in_file` and
  `execute_command` are read under the names the converter gives them (`Read`,
  `Write`, `Edit`, `Bash`), so they become files, wrote, edit and command
  records; a command carries the `exitCode` its result reported. Results are
  indexed as tool output: a command's stdout and stderr, a file's content, an
  MCP tool's text. A result with none of these is indexed as the converter
  writes it: a file list or a search with no match in the converter's words,
  anything else as the result's JSON with sorted keys, so an edit or a write
  leaves an output before the upgrade as after it.
- **Compaction**: a summary the IDE wrote in place of earlier turns
  (`isMaxTokenLimitSummary`) is skipped; the turns are in the files. The IDE
  deletes the message files of the turns a compaction replaced and keeps
  their ids in `index.json`; a file missing before a compaction message
  (`isMaxTokenLimitSummary`, `isSummary` or `isCompacted`) is not reported
  as unreadable, and one missing after the last is.
- **Team members** — conversations listed with type `team-member` — are read as
  sub-agents, under `DEJA_INCLUDE_SUBAGENTS` like every other child run.
- **Model and tokens** come from each assistant message's `modelId` and each
  request's `usage`, with `cacheTokens` counted as cache reads.

**Upgraded conversations are read once.** When the 5.x IDE opens a 4.x
conversation it writes a CodeBuddy Code transcript under the same id, with a
marker beside it naming the conversation it came from
(`<id>/.codebuddy-ide-4-conversion.json`), and leaves the old directory where
it was. While that transcript is on disk and not empty, it is the conversation —
it holds every turn the old one did and every turn after the upgrade — and the
4.x copy is not read. Without the marker, or with an empty transcript, the 4.x
copy is read again, and the index drops what it held from the transcript. The
5.x catalog database is not consulted, so a machine with only 4.x installed
reads its whole history. A CodeBuddy Code session that shares an id with a 4.x
conversation without the marker is a different conversation, and both are kept.
A team member of a converted conversation goes into the lead's `subagents/`
and is read from there. On a real store, every converted conversation reads the
same user turns from the 4.x copy as from its transcript.

**Updates.** The index reads a conversation again when its `index.json`, its
`messages/` directory, the size or time of any of its message files, its entry
in the workspace index, or the transcript that stands in for it changes. A
message rewritten in place to the same size within the filesystem's clock
resolution is not seen until something else changes; the IDE rewrites a
message while its reply streams, which grows it.

**Resume.** `codebuddy -r` cannot open a 4.x conversation. Opening it in IDE
5.x converts it, after which `deja resume` prints the command for the
transcript, from the directory the transcript ran in; `deja show` has the
conversation either way. `--write-back` restores a deleted CodeBuddy Code
transcript and does not write a 4.x conversation.

**Remote history.** A Remote-SSH workspace keeps its 4.x history on the remote
machine under `~/.local/share/CodeBuddyExtension/Data`. Index it there, and
bring it over with `deja sync ssh`, or point `DEJA_CODEBUDDY_LEGACY_ROOTS` at a
copy.

Checked against a real 4.x store holding conversations from several workspaces,
some of them already converted by 5.x, and against the converter in the 5.x
IDE's genie extension. The fixture is synthetic.
