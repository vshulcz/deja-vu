# Qwen Code session format

## Store and files

Qwen Code writes sessions below `${DEJA_QWEN_ROOT:-~/.qwen}/projects/<encoded-project>/chats/*.jsonl`. The project directory uses the same slash-and-hyphen encoding as Claude Code. The `chats/` directory is part of Qwen's layout; only JSONL files directly inside it are session streams. A sub-agent's log, `subagents/<session>/agent-<id>.jsonl` in the same project directory, is read only with `DEJA_INCLUDE_SUBAGENTS=1`, as a session of its own naming the parent session (#4483).

The Qwen configuration directory remains `~/.qwen` for installer settings. `DEJA_QWEN_ROOT` relocates session reads only.

## Records

Each line is a JSON object with `type`, `sessionId`, `timestamp`, and a `message` object. Message text is in `message.parts`; parts marked `thought: true` are reasoning and are excluded.

```json
{"type":"assistant","sessionId":"session-7","timestamp":"2026-07-17T09:00:01Z","message":{"role":"model","parts":[{"text":"The failing check is in parser.go."}]}}
```

`message.role` `model` maps to `assistant` and `user` maps to `user`; when it is absent, the top-level `type` is used. Parts with text are joined with newlines. RFC 3339 and numeric Unix timestamps are accepted.

The work sits in the same `parts` list as `functionCall` and `functionResponse`. Calls become command, file and edit records (Qwen uses Gemini's tool names), and a `tool_result` record's `functionResponse` becomes tool output.

## Wiring

- **MCP**: `deja install qwen` adds `mcpServers.deja` to `~/.qwen/settings.json`.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`. Qwen lists skills under `/skills`, so the skill is also the command.
- **Auto-recall**: `deja install qwen-auto` adds hooks to the same `settings.json`: `SessionStart` (digest), `UserPromptSubmit` (per-prompt recall), `PostToolUseFailure` on `run_shell_command` (the earlier fix for a failed command), `PostToolUse` on `read_file|edit|write_file` (the line deja keeps for that file), `PreCompact` and `SessionEnd` (the session you quit is back in the next one's MCP recall).

## Resume

`qwen -r <id>` reopens a session by id, and the id is the chat file's name —
the same one deja indexes. The command has to run in the project directory:
`qwen sessions list` shows only the current project's sessions, so from
anywhere else the id resolves to nothing. deja takes that directory from the
`cwd` the transcript records, or from the encoded folder name for a transcript
that records none, and prints `cd <project> && qwen -r <id>`. When the recorded
directory is gone, or deja cannot tell which directory the session ran in,
resume refuses and points at `deja show <id>`: the bare command would only get
"No saved session found". On the screen bare `deja` opens, `r` runs the same
command for the picked session and `o` continues it in another agent.

## Status line

`deja install qwen-auto` sets `ui.statusLine` in `settings.json` to a command
with `refreshInterval: 1`. Qwen pipes `session_id` and the workspace but no
transcript path, so deja finds the session by its id. A status line already
set there is left alone. Rendered live on 0.20.0.

## Known quirks and drift

- JSONL can end in a partial line while Qwen is writing. Malformed lines are skipped.
- System and other control records do not become messages.
- Project path encoding is ambiguous because `-` represents both a separator and a hyphen. deja checks the local filesystem before using a two-segment fallback.

**Last verified:** 2026-09-07
