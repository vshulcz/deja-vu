# GigaCode CLI session format

## Store and files

GigaCode CLI writes sessions below `${DEJA_GIGACODE_ROOT:-${GIGACODE_HOME:-~/.gigacode}}/projects/<encoded-project>/chats/*.jsonl`. The project directory uses the same slash-and-hyphen encoding as Claude Code and Qwen Code. The `chats/` directory is part of GigaCode's layout; only JSONL files directly inside it are session streams. A sub-agent's log, `subagents/<session>/agent-<id>.jsonl` in the same project directory, is read as a session of its own naming the parent session: the task, the files it changed and its last turns. `DEJA_INCLUDE_SUBAGENTS=1` takes the whole run and `=0` skips them.

The GigaCode configuration directory defaults to `~/.gigacode` and honors `GIGACODE_HOME` for installer settings. `DEJA_GIGACODE_ROOT` relocates session reads only.

## Records

Each line is a JSON object with `type`, `sessionId`, `timestamp`, and a `message` object. Message text is in `message.parts`; parts marked `thought: true` are reasoning and are excluded.

```json
{"type":"assistant","sessionId":"session-7","timestamp":"2026-11-25T09:00:01Z","message":{"role":"model","parts":[{"text":"The failing check is in parser.go."}]}}
```

`message.role` `model` maps to `assistant` and `user` maps to `user`; when it is absent, the top-level `type` is used. Parts with text are joined with newlines. RFC 3339 and numeric Unix timestamps are accepted.

Tool work sits in the same `parts` list as `functionCall` and `functionResponse`. Calls become command, file and edit records, and a `tool_result` record's `functionResponse` becomes tool output.

## Wiring

- **MCP**: `deja install gigacode` adds `mcpServers.deja` to `${GIGACODE_HOME:-~/.gigacode}/settings.json`.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`. GigaCode lists skills under `/skills`, so the skill is also the command.
- **Rules**: `deja rules sync` writes shared rules to `${GIGACODE_HOME:-~/.gigacode}/GIGACODE.md`.
- **Auto-recall**: `deja install gigacode-auto` adds hooks to the same `settings.json`: `SessionStart` (digest), `UserPromptSubmit` (per-prompt recall), `PostToolUseFailure` on `run_shell_command` (the earlier fix for a failed command), `PostToolUse` on `read_file|edit|write_file` (the line deja keeps for that file), `PreCompact` and `SessionEnd` (the session you quit is back in the next one's MCP recall).

## Resume

`gigacode -r <id>` reopens a session by id, and the id is the chat file's name —
the same one deja indexes. The command has to run in the project directory, so
deja takes that directory from the `cwd` the transcript records (when its
encoded project folder matches), or from the encoded folder name for a
transcript that records none, and prints `cd <project> && gigacode -r <id>`.
When the recorded directory is gone, or deja cannot tell which directory the
session ran in, resume refuses and points at `deja show <id>`. On the screen
bare `deja` opens, `r` runs the same command for the picked session and `o`
continues it in another agent.

## Status line

`deja install gigacode-auto` sets `ui.statusLine` in `settings.json` to a
command with `refreshInterval: 1`. GigaCode pipes `session_id` and the workspace
without a transcript path, so deja resolves the session by its id. A status line
already set there is left alone.

## Known quirks and drift

- JSONL can end in a partial line while GigaCode is writing. Malformed lines are skipped.
- System and other control records do not become messages.
- Project path encoding replaces every non-ASCII-alphanumeric rune with `-`, so paths with hyphens or non-ASCII characters depend on recorded `cwd` (verified on 26.11.141).

**Last verified:** 2026-11-25
