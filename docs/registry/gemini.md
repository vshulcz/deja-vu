# Gemini CLI session format

## Store and files

Gemini CLI stores chats under `~/.gemini/tmp/<project-id>/chats/`. If `GEMINI_CLI_HOME` is set, Gemini appends `.gemini` to that directory. deja can override only session reads with `DEJA_GEMINI_ROOT`.

Two generations coexist: whole-session `.json` files and replayable `.jsonl` files. `projects.json` maps working directories to project IDs; a per-project `.project_root` file is another observed source for the project name.

## Records

Whole-session JSON has `sessionId`, `startTime`, `lastUpdated`, and a `messages` array. JSONL puts the session fields on its first metadata line, followed by message and control lines.

```json
{"sessionId":"session-7","startTime":"2026-07-17T09:00:00Z","lastUpdated":"2026-07-17T09:00:02Z"}
{"id":"message-1","timestamp":"2026-07-17T09:00:01Z","type":"gemini","content":[{"text":"The configuration is valid."}]}
```

`type: "user"` maps to `user`; `gemini` and `model` map to `assistant`. Other types, including informational and error events, are ignored. Content is a string or an array of parts with `text`. All documented timestamps are RFC 3339; a missing message timestamp falls back to session start.

## Wiring

`deja install gemini` adds the server under `mcpServers` in `~/.gemini/settings.json` and writes the shared skill `~/.agents/skills/deja-history/SKILL.md`. There is no command file: Gemini's command namespace is flat, the MCP server's own prompt is already `/deja`, and each skill is listed as a command too. `deja install gemini-auto` adds the same plus an extension at `~/.gemini/extensions/deja/`, because Gemini loads hooks from extensions and not from `settings.json`, and only with `hooksConfig.enabled` set, which install turns on. Uninstall takes the switch back out when install added it and no other extension has hooks; otherwise it stays on and uninstall says so. A `false` you set is said aloud when install turns it on, and uninstall sets it back to `false`. The extension wires `SessionStart` (`deja hook-context`), `BeforeAgent` (`hook-prompt`), `AfterTool` on `run_shell_command` (`hook-tool-after`) and `SessionEnd` (`hook-session-end`, which tells MCP recall the session is over and may be answered with); timeouts are in milliseconds.

## Resume

`gemini --resume <uuid>` takes the session id deja indexes — the `--help` text
mentions only `latest` and an index, but the CLI's own error names
`--resume {uuid}` and 0.55.1 accepts one. gemini finds a session only from
the directory it ran in — anywhere else it says "No previous sessions found
for this project" — so the command runs there. 0.60 records that directory
in `~/.gemini/projects.json` and in the project folder's `.project_root`; a
store that keys the folder by a hash of the path alone gets no `cd`, and a
recorded directory that is gone is refused with a pointer to `deja show`.

## Known quirks and drift

- A resumed legacy `.json` session can be rewritten as `.jsonl`. deja deduplicates by session ID and prefers JSONL.
- JSONL `$set.lastUpdated` patches session metadata.
- `$rewindTo` replays history by removing the referenced message and everything after it before later records are applied.
- The chats tree also contains non-session JSON such as checkpoints; files without a valid session ID and messages are ignored.
- Nested subagent chat files occur below a parent chat directory.

**Last verified:** 2026-07-24
