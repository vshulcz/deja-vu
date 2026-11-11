# Qoder session format

## Store and files

Qoder — Alibaba's AI IDE and CLI — writes CLI sessions below
`${DEJA_QODER_ROOT:-~/.qoder}/projects/<encoded-project>/<session-uuid>.jsonl`.
The project directory uses the same slash-and-hyphen encoding as Claude Code
(`-Users-x-code-app`). Unlike Qwen Code there is no `chats/` level: the
transcript sits directly in the project directory. Lines carry an
`isSidechain` flag, the field Claude Code uses to mark sub-agent turns inside
the session file itself.

The Qoder configuration directory remains `~/.qoder` for the CLI's own
settings (`agents/`, `commands/`, hooks, ...). `DEJA_QODER_ROOT` relocates
session reads only.

The Qoder **IDE** (a VS Code fork) keeps its chats elsewhere: as
`aicoding-chat-<uuid>` entries in
`~/Library/Application Support/Qoder/User/globalStorage/state.vscdb` plus an
`aicoding.aicoding-agent` global-storage directory. That store is not parsed
today; this entry covers the CLI's transcripts only.

## Records

Each line is Claude Code-shaped JSON: `type` (`user`, `assistant`,
`progress`, ...), `sessionId`, `uuid`, `parentUuid`, `timestamp`, `cwd`,
`version`, `isSidechain`, `permissionMode`, and a `message` object whose
`content` is a string or a list of content blocks. Observed on Qoder CLI
transcripts written from 2026-04 onward.

```json
{"type":"user","sessionId":"3f2b7c1e-0a5d-4b62-9e84-1c2d3e4f5a6b","cwd":"/workspace/demo","timestamp":"2026-07-17T09:00:00Z","uuid":"a0000000-0000-4000-8000-000000000001","parentUuid":null,"isSidechain":false,"version":"1.0.0","message":{"role":"user","content":"..."}}
```

The Claude decoder reads these lines, so role mapping, timestamp handling,
tool records and side-chain behaviour are exactly as described in
[claude-code.md](claude-code.md); only the harness attribution differs —
sessions are indexed as `qoder`, never as `claude`. `progress` lines (hook
events such as `SessionStart`) carry no message and are skipped like their
Claude counterparts.

## Wiring

Nothing is wired yet: no MCP install target, no auto-recall hooks, no skill
or command install, and no resume support. This entry covers the reader —
search, recall, blame and digest over indexed Qoder history work because the
sessions are ordinary transcripts once parsed. The Qoder CLI's hook and MCP
surface has not been verified; wiring is follow-up work.

## Resume

Not supported. deja does not know a Qoder command that reopens a session by
id, so handoff falls back to paste.

**Last verified:** 2026-11-10
