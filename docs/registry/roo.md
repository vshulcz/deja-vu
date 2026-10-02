# Roo Code

- **ID**: `roo`
- **Store**: VS Code-host globalStorage `rooveterinaryinc.roo-cline/tasks/<taskId>/api_conversation_history.json`; per-task metadata in `history_item.json` (id, ts, task, workspace). Code, Code Insiders, VSCodium, Cursor and Windsurf host roots are probed, and a `roo-cline.customStoragePath` set in a host's `User/settings.json` is followed. The Roo CLI writes the same tree under `~/.vscode-mock/global-storage`.
- **Read override**: `DEJA_ROO_ROOTS` (path list); `DEJA_ROO_CLI_ROOT` for the CLI's storage base
- **Format**: whole-file JSON rewritten on change; full re-parse per pass. A change to `history_item.json` alone re-reads the task too (#4446)

The transcript shape matches Cline's legacy store (Roo is a Cline fork), so
the same text-block extraction and `<task>` envelope unwrapping apply.
Format verified against Roo-Code source (`src/shared/globalFileNames.ts`,
`src/core/task-persistence`) and against a live run of the Roo CLI, which
drives the same extension against a VS Code shim and writes the same files
under `~/.vscode-mock/global-storage`.

An edit call carries no `old_string`. `apply_diff`, and `replace_in_file` in the
legacy shape, pass a SEARCH/REPLACE block under `diff`; `search_and_replace`
names the two sides `search` and `replace`; `write_to_file` and `insert_content`
carry the written side alone under `content`. Both sides are read where both are
there — the SEARCH body is the replaced span `deja restore` hands back, the
REPLACE body becomes the hashed written lines line-level blame matches. A
`search_and_replace` with `use_regex` records neither side, because a pattern is
not text the file held, and a block whose closing marker never arrives records
nothing rather than guessing where it ended. Current Roo also offers
`search_replace`, `edit_file` and `edit`, which take `old_string` and
`new_string` under `file_path`, and `apply_patch`, whose paths and `-`/`+` lines
are read out of the patch body.

An `execute_command` result opens with its status, "Command executed in
terminal within working directory '…'. Exit code: N" (a failure puts "Command
execution was not successful, …" there and the code on the next line), and the
command record carries `→ exit N` from it. A command killed by a signal, or one
whose code the terminal never reported, keeps no code (#4530).

Tasks from before native tool calling (Roo 3.20, and the legacy Cline
extension) keep each call as XML inside the assistant's text block —
`<execute_command><command>…</command></execute_command>` — and its result as
user text blocks headed `[execute_command for '…'] Result:`. Those calls give
the same records a `tool_use` block does, and the result is indexed as tool
output, not as the person's words. Only the first call of a message counts,
since the client ran no other, and only in a task with no `tool_use` block at
all: in a native-era task XML in the text is something the model showed. The
answer to `ask_followup_question`, the feedback on `attempt_completion` and any
`<feedback>` typed beside a result stay the person's. The retry prompt the
client sends when the model used no tool ("[ERROR] You did not use a tool…") is
not indexed as a user turn.

A call names its file relative to the workspace, so the path is resolved against
the `workspace` in `history_item.json` before it is recorded — a one-segment
path like `loop.go` matches no file on any machine otherwise. A task without
that metadata keeps the path as it was recorded rather than resolving it against
the wrong root.

- **MCP**: `deja install roo` writes the server into `mcp_settings.json` for
  every host Roo has run in, and names deja's own tool in that entry's
  `alwaysAllow` — without it Roo asks before every recall.
- **Skill**: the shared `~/.agents/skills/deja-history/SKILL.md`.
- **Command**: `~/.roo/commands/deja.md`, invoked as `/deja`.
- **Auto-recall**: none; Roo has no released lifecycle hooks.
- **Resume**: `roo -w <workspace> --session-id <uuid>`, run in the task's
  workspace, for tasks the CLI created. The `-w` matters: without it the CLI
  looks under the real path of its cwd, and a task created with `-w /tmp/...`
  on macOS recorded the symlinked path. A workspace that is gone is refused
  with `deja show` (#4459). Editor tasks reopen from the extension's history UI.
- **Handoff**: paste.

**Last verified:** 2026-09-07
