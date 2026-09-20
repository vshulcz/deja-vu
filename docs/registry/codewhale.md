# CodeWhale

- **ID**: `codewhale`
- **Store**: `${CODEWHALE_HOME:-~/.codewhale}/sessions/<id>.json` — one file per session
- **Store (pre-rebrand)**: `~/.deepseek/sessions/<id>.json`, which CodeWhale migrates into the current root on first access
- **Read overrides**: `DEJA_CODEWHALE_ROOT` replaces the session directory; `CODEWHALE_HOME` moves the whole store
- **Format**: JSON — `{schema_version, metadata, messages}`, pretty-printed
- **Needs**: nothing
- **Resume**: `codewhale --resume <id>`, run in the workspace the session was worked in — `--session-id` is its alias and `codewhale exec` takes both (checked against 0.9.13)

CodeWhale is a terminal agent written in Rust. It shipped as `deepseek-tui`
until v0.8.41 and under its own name since; the provider integration did not
change with the name. It is not the DeepSeek Harness deja reads as `deepseek` —
a different program with a different store, which is why both entries exist.

`metadata` carries the id, the title CodeWhale derived itself, `created_at`,
`updated_at`, the `workspace` the session was worked in, and
`parent_session_id` when the session came from a fork. A message is
`{role, content}`, where content is the block list Anthropic's API uses —
`text`, `thinking`, `tool_use`, `tool_result` — so the decoding the Cline and
Claude readers already do applies here: a call becomes a command or a file
record, a `tool_result` becomes tool output, error runs included.

`thinking` blocks are dropped. So are the `system` and `developer` roles:
CodeWhale's own documentation names them as where it puts compaction summaries,
branch summaries and sub-agent framing, which is the harness talking to itself
rather than anything either side said.

`$CODEWHALE_HOME` is an isolation boundary in CodeWhale's own resolver — with it
set, the legacy root is not consulted — and deja does not reach outside it
either.

**Last verified:** 2026-09-20

## Known quirks and drift

- **No per-message timestamps.** The file stores `created_at` and `updated_at`
  for the session and nothing per message. The session's own start is the clock,
  one millisecond per record, so two identical turns stay two records rather
  than collapsing into one the way they did for Zed (#3333).
- **Bookkeeping sits beside the transcripts.** `offline_queue.json`,
  `owners.json` and the `checkpoints/` slot share the sessions directory; they
  are named rather than counted, so drift in the store still shows up as an
  unread file.
- **The shapes come from the source, not from a running install.** The store
  layout, the role list and the tool names are read out of CodeWhale's own
  crates — `session_manager.rs`, `core/src/request.rs`, `core/src/role.rs`,
  `tui/src/tools/registry.rs` — and its `docs/REBRAND.md`. Nothing here has been
  checked against a live CodeWhale on this machine.
- **Resume is real, the store shape is not checked live.** `codewhale --help` on
  0.9.13 lists `--resume`, `--session-id` and `--continue`, and `--continue`
  refuses in a directory with no saved session, which is how the per-workspace
  scoping shows. `codewhale exec` does not persist a session at all — the TUI
  writes them — so the file layout above is still read from the source rather
  than from one the binary wrote here.
- **Nothing is wired.** deja reads this store and writes nothing into CodeWhale.
  Its MCP server map, its hook block and its plugin marketplace are each a
  channel an install could use; none is written yet.
