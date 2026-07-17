# aider session format

## Store and files

aider appends Markdown to `.aider.chat.history.md` in its launch directory. The default home file is `~/.aider.chat.history.md`; `AIDER_CHAT_HISTORY_FILE` relocates it. deja also scans each directory in the platform path-list variable `DEJA_AIDER_ROOTS`, to two levels below each root.

One history file contains multiple sessions. A session begins with:

```markdown
# aider chat started at 2026-07-17 09:00:00

#### Show me the failing query

The query misses the tenant predicate.

> Applied edit to query.go
```

## Message mapping

Outside fenced code blocks, `#### ` starts or continues a user message. Plain Markdown is assistant output. Lines beginning with `> ` are tool or system output and are not indexed. Blank lines remain part of the current message.

The header timestamp uses local time with layout `YYYY-MM-DD HH:MM:SS`. aider does not store message timestamps, so every message receives the session start. It does not store a session ID; deja derives a stable ID from the history path and the session's ordinal in that file.

## Known quirks and drift

- The file is append-only and can contain many launches.
- Markdown fences may contain lines beginning with `#### ` or `>`; these are content, not role markers. The parser tracks triple-backtick fences.
- `<blank>` after the user prefix represents an empty line.
- Tool output terminates an assistant block but does not become a message.
- Moving a history file changes deja's derived session IDs because the path is part of the ID.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
