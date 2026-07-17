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

## Known quirks and drift

- A resumed legacy `.json` session can be rewritten as `.jsonl`. deja deduplicates by session ID and prefers JSONL.
- JSONL `$set.lastUpdated` patches session metadata.
- `$rewindTo` replays history by removing the referenced message and everything after it before later records are applied.
- The chats tree also contains non-session JSON such as checkpoints; files without a valid session ID and messages are ignored.
- Nested subagent chat files occur below a parent chat directory.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
