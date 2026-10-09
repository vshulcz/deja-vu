# CodeBuddy Code

- **ID**: `codebuddy`
- **Store**: `${CODEBUDDY_CONFIG_DIR:-~/.codebuddy}/projects/<mangled-cwd>/<session-id>.jsonl` — one file per session
- **Sub-agents**: `<mangled-cwd>/<session-id>/subagents/agent-<id>.jsonl`, read with `DEJA_INCLUDE_SUBAGENTS=1`
- **WorkBuddy**: `~/.workbuddy/projects/...` (workbuddy.cn) and `~/.workbuddy-ai/projects/...` (WorkBuddy AI, workbuddy.ai), or `$WORKBUDDY_CONFIG_DIR/projects`; the same layout and records, read when they exist
- **Read overrides**: `DEJA_CODEBUDDY_ROOTS`, a path list, replaces both stores
- **Format**: JSONL — OpenAI Responses-style items
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

**Last verified:** 2026-10-05

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
- **The IDE store is separate.** The IDE and the VS Code extension keep their
  history under `CodeBuddyExtension/Data/...` in the platform data directory,
  never in this tree. Not read yet (#4681).
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
