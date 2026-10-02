# Senpi

- **ID**: `senpi`
- **Store**: `${SENPI_CODING_AGENT_DIR:-~/.senpi/agent}/sessions/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_SENPI_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Wiring**: `deja install senpi` writes the server into `<agent>/mcp.json`;
  `deja install senpi-auto` adds the extension at `<agent>/extensions/deja.ts`,
  which is both the injection point and the `/deja` command
- **Needs**: nothing

Senpi (OmO Native) descends from pi and kept its transcript envelope — a
`session` header line, then one `message` line per turn — so the parsing is
pi's. The header's `cwd` names the project; the encoded directory is the
fallback for a header without one, because Senpi folds every `/` into `-` and
`/tmp/my-app` and `/tmp/my/app` share a folder name (#4427).

It kept the rest of pi too, which is what closes the harness: on a live install
of `@code-yeongyu/senpi` every surface answered on senpi's own screen.

- `<agent>/mcp.json` with `mcpServers` is loaded — its `/` palette lists
  `mcp:deja:deja` with the server's own description.
- Skills load from the shared `~/.agents/skills` **and** from `<agent>/skills`.
  Both at once is a fault senpi announces: `"deja-history" collision: ✓
  <agent>/skills ✗ ~/.agents/skills (skipped)`. So deja writes the shared copy
  only, and takes the local one back out if an older install left it there.
- pi's extension loads unchanged, and with it the `/deja` command the palette
  shows as "Search your own past coding sessions".
- A session started with that extension records what `deja hook-context`
  returned as `{"type":"custom_message","customType":"deja-recall"}` — which is
  auto-recall arriving, in senpi's own transcript.
- `--session <path|id>`, `--resume` and `--fork` are in its own help, so
  `deja resume` prints `senpi --session <id>`, after a `cd` into the
  directory the header records: from anywhere else Senpi offers to fork the
  session into the current directory instead of reopening it (#4426).

**Last verified:** 2026-09-17

## Known quirks and drift

- **Commands run inside `eval` cells.** With codemode loaded, Senpi's
  default, `bash`, `grep` and `powershell` leave the model's tool list and run
  only as `tool.bash(...)` in a cell. The transcript's call is then `eval`
  with JS, and what ran is on the result, under
  `details.toolCalls[].args`; deja reads the commands, files and edits from
  there. The cell's text is a JSON string, `{"text":"…"}`, and is read as the
  text it wraps (#4425).
- **Senpi's first run moves `~/.pi/agent` to `~/.senpi/agent`** — the whole
  directory, sessions and config and extensions, and it prints one line about
  it. So installing senpi on a machine that has pi leaves pi's own directory
  empty: pi then starts with no server, no extension and no history, while the
  same files answer as senpi's. Both rows are in `deja doctor` and the sessions
  stay searchable either way, but a pi user who wants both clients has to put
  pi's copy back.
- After such a migration the same transcripts can exist under both roots, and
  deja counts them per harness — so one session can appear twice in a result,
  once as pi and once as senpi.
- `SENPI_CODING_AGENT_DIR` moves the whole agent directory, sessions included,
  so deja reads it: a machine that has already said where its sessions are
  should not have to say it twice in a second variable.
- Senpi's terminal extension keeps per-session state at
  `sessions/<project>/extensions/terminal/<id>.json`. Those are not transcripts
  and deja does not count them as unread ones.
