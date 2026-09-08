# Crush

- **ID**: `crush`
- **Store**: `<project>/.crush/crush.db`, one SQLite store per project, listed in `${XDG_DATA_HOME:-~/.local/share}/crush/projects.json`.
- **Read override**: `DEJA_CRUSH_ROOT` (the data home holding that registry)
- **Format**: SQLite; `sessions` joined to `messages`, incremental by `sessions.updated_at`.

Crush is the only harness here that keeps its history beside the work rather
than in one place. Nothing under the data home holds a transcript: the registry
does, as `{"projects":[{"path","data_dir","last_accessed"}]}`, and `crush
projects` prints the same pairs. A registry entry whose project has since been
deleted is ordinary and is skipped rather than reported.

A message's `parts` column is a JSON array of `{type, data}`. `text` carries
`data.text`; `tool_call` carries the tool's name and a JSON *string* of its
arguments, so the shell command and the edited path are one decode further in;
`tool_result` carries what the tool printed with a `<cwd>…</cwd>` tag appended,
which is the same directory on every line in the store and is stripped rather
than indexed — left in, a search for the project name would match every tool
output there is. `finish` is bookkeeping.

Both stamp columns are commented "Unix timestamp in milliseconds" in Crush's own
schema and hold whole seconds in what v0.92.0 writes — its update trigger sets
`strftime('%s','now')`. Both units are read. The project is the directory the
store sits under; Crush records no working directory on the session row.
`parent_session_id` is the only place the subagent edge exists, so a subagent's
turns are filed under their parent rather than as separate work.

Shape verified against crush v0.92.0 by running one against a recording
endpoint: the store landed beside the project, the registry gained its path and
`data_dir`, and deja indexed the result.

- **MCP**: `deja install crush` adds the server under `mcp` in
  `~/.config/crush/crush.json` — Crush keys it `mcp`, not the common
  `mcpServers`. Crush's own `crush_info` reports `deja = connected (1 tools, 0
  resources)`, and the tool arrives as `mcp_deja_deja`. Verified against the
  recorder: a scripted call came back with the seeded decision in the bytes
  Crush sent next.
- **Skill**: the same install writes the shared
  `~/.agents/skills/deja-history/SKILL.md`, which Crush scans by default
  alongside `~/.claude/skills` and its own directory. It appears in the system
  prompt's `<available_skills>` block and in `crush_info` as `deja-history =
  user`.
- **Command**: `~/.config/crush/commands/deja.md`, which Crush lists as
  `/user:deja`. The palette is a TUI surface, so the entry is written but its
  expansion is unverified here.
- **Auto-recall**: `deja install crush-auto` adds a `PreToolUse` hook —
  `deja hook-tool --crush`, matcher `^(bash|edit|write|multiedit)$`. `PreToolUse`
  is the only event Crush fires, so there is no session-start or per-prompt
  channel: the command-and-file line is the whole of it. A hook answers with a
  flat `{"version":1,"context":…}` rather than Claude's nested envelope, which
  is what the `--crush` flag writes; no `decision` field, because `"allow"`
  there is affirmative pre-approval and would skip the permission prompt on
  every call the hook fires on. Measured: with a scripted `go build ./...`
  against a store holding that command's history, the recall block reached the
  model appended to the tool result, in the request Crush sent next.
- **Resume**: `crush --session <uuid>`, run in the project directory — Crush
  looks for the session in the store under the current directory and nowhere
  else, so the same id resolves to nothing from elsewhere.
- **Handoff**: exec, `crush run`.

Costs on that version, measured from the recorded requests: deja's tool schema
is 3,138 bytes of the 29,754-byte tool block in every request, the skill's
catalogue entry adds 384 bytes to the 22,487-byte system prompt, and a recall
that answers adds 269 bytes to the turn that asked. With the binary moved aside
and the wiring left in place, the turn still completed: 28 tools instead of 29,
the hook's failure logged and ignored, the tool call through.

**Last verified:** 2026-09-07
