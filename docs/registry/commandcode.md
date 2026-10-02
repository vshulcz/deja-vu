# Command Code

- **ID**: `commandcode`
- **Store**: `~/.commandcode/projects/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_COMMANDCODE_ROOT` replaces the project root
- **Format**: JSONL — a `session` header, then one `message` envelope per line
- **Needs**: nothing
- **Resume**: `cmd --resume <id>` (`cmdc` on Windows), run in the header's `cwd`, since it finds a session only under the current directory's project (#4372). With that directory gone, or none recorded, `cmd --session <id>`, which searches every project and continues the same transcript where you run it (checked against 1.73.4, #4460)

Command Code writes a Claude-Code-style project directory with one transcript
per session. Since 1.73 (format version 3) the first line is a header,
`{"type":"session","version":3,"id","timestamp","cwd"}`, and every line after
it is `{"type":"message","timestamp",…,"message":{"role","content"}}`, the
content in Claude's blocks (`text`, `tool_use`, `tool_result`). The project is
the header's `cwd`; the directory name is a lossy slug of it.

Tool calls use Command Code's own names with Claude's input keys:
`shell_command` (`command`), `read_file`, `edit_file` (`file_path`,
`old_string`, `new_string`), `write_file` (`file_path`, `content`) and
`read_multiple_files` (`paths`). All of them are indexed as commands, files,
replaced spans and written lines (#4370). A failed command's `tool_result`
opens with "Exit code: N" (or "Exit code: N (No matches found)" and the like)
and has no `is_error`; that code goes on the command the result answers as
`→ exit N`. A clean run writes no such line and gets no code. A result that
lands in a later index pass than its call is read with the call (#4539).

The older shape, one flat `role`/`content`/`timestamp`/`sessionId` line per
message, is still read. The client migrates such a file to v3 in place the
next time it opens the session and keeps the old copy as `<session>.v2.bak`.

**Last verified:** 2026-10-01 (command-code 1.73.4)

## Known quirks and drift

- **A sibling with the same extension.** `<session>.checkpoints.jsonl` sits
  beside the transcript and is a snapshot stream, not a conversation — read as
  one it adds a session with no words in it that then competes for a recall
  slot. It is skipped by name, and a test pins that. So are
  `<session>.prompts.jsonl` and `<session>.v2.bak`, which is the client's own
  transcript filter, and `doctor` counts them and the `<session>.meta.json`
  sidecar as files beside the transcripts rather than as unread.
- A line whose role is neither `user` nor `assistant` is tool output, which is
  where a command's error text lives. It is indexed as tool output rather than
  dropped, so a user can search for an error they have already hit.
- Wiring: `deja install commandcode` writes all four of its user-scoped
  surfaces, and `commandcode-auto` adds the hooks —
  `~/.commandcode/mcp.json` for the server, `skills/deja-history/SKILL.md`,
  `commands/deja.md` for `/deja`, and a `hooks` key in `settings.json` in
  Claude's shape.
- **Two things there fail quietly if copied from another harness.** The
  timeout unit is seconds, not milliseconds — the numbers deja passes to
  other settings.json targets would be ten minutes each, past the documented
  600-second maximum. And the tool matcher is a regex over Command Code's own
  display names — `SHELL`, `READ`, `EDIT`, `WRITE`, `SEARCH`, `GLOB`, `LIST`
  — so a Claude-shaped `Bash` matcher never fires at all. Both are pinned by
  tests.
- There is no per-prompt event: the four documented are `SessionStart`,
  `PreToolUse`, `PostToolUse` and `Stop`, so the digest rides SessionStart and
  the rest is the tool-time pair.
- The transcript format above was read off files command-code 1.73.4 wrote
  and its bundle (`toStoredEntry`, `parseV3Lines`, `migrateToV3`). The wiring
  paths come from rulesync's `commandcode-paths.ts` and `types/hooks.ts`, and
  akitaonrails/ai-memory's support matrix.
