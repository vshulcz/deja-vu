<p align="center"><img src="assets/logo.svg" width="340" alt="deja-vu"></p>

<p align="center"><strong>Your agents already solved this. deja finds it.</strong></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">vshulcz.github.io/deja-vu</a></p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://www.npmjs.com/package/@vshulcz/deja-vu"><img src="https://img.shields.io/npm/v/%40vshulcz%2Fdeja-vu?label=npm" alt="npm"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/vshulcz/deja-vu"><img src="https://api.scorecard.dev/projects/github.com/vshulcz/deja-vu/badge" alt="OpenSSF Scorecard"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><img src="assets/demo.gif" alt="deja demo"></p>

Claude Code, Codex, opencode, aider, Gemini CLI, Cursor, Antigravity and Grok Build write every conversation to local files — gigabytes of debugged problems and design decisions you can't search. deja is a zero-dependency binary that turns those histories into a memory layer:

| | |
| --- | --- |
| **Search** | `deja "connection pool exhausted"` — 7–9 ms over gigabytes, retroactive: months of logs from before you installed it |
| **Agent recall** | MCP `recall` tool — the agent answers *"we fixed this three weeks ago"* instead of re-debugging, across harnesses |
| **Auto-recall** | `install --auto` adds a SessionStart hook: relevant memory lands in context before you ask |
| **Redaction** | API keys, JWTs, private keys are stripped at index time — the cache is safe to keep |
| **Stats** | `deja stats` — your agent work, wrapped: harnesses, top projects, activity sparkline |
| **Share** | `deja share <id>` — hand a colleague a sanitized digest of a session, secrets already scrubbed |
| **Sync** | `deja sync export/import` — move memory between machines, append-only, idempotent |

One binary. No models to download, no services to run, nothing leaves your machine. (opencode and Cursor IDE indexing shell out to the `sqlite3` CLI, preinstalled on macOS and most Linux distros; Cursor CLI transcripts do not need it.)

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

or:

```sh
go install github.com/vshulcz/deja-vu/cmd/deja@latest   # Go
npx @vshulcz/deja-vu "query"                            # npm, no install
brew install vshulcz/tap/deja-vu                        # Homebrew
```

Wire it into the agents you use (edits config, keeps a `.bak`):

```sh
deja install --all          # MCP recall for every agent it finds on this machine
deja install claude-code --auto   # + SessionStart auto-recall hook
```

That's it. Next session, ask your agent:

> have we dealt with jwt refresh rotation before? check your memory

— or with `--auto`, don't ask: the agent starts each session already knowing what you solved in that project.

## CLI

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Command | What it does |
| --- | --- |
| `deja <query>` | Search all histories. Multi-word = AND, substrings match (`code` finds `opencode`). `--re`, `--harness`, `--project`, `--since 30d`, `--role`, `--json`. |
| `deja ctx <query>` | Compact markdown digest of the best match — pipe it into a prompt. |
| `deja share <id>` | Sanitized session digest for a colleague: secrets redacted, tool noise stripped. |
| `deja stats` | Totals, per-harness split, top projects, monthly sparkline. `--json` too. |
| `deja sync export <dir> [--full]` / `import <dir>` / `ssh <host> [--pull]` | Move memory between machines — via a shared folder or one ssh command. Watermarked, append-only, idempotent. |
| `deja show <id>` / `deja last [n]` | Read one session / list recent ones. |
| `deja resume <id> [--exec]` | Reopen a found session in its native harness (`claude --resume`, `codex resume`, `opencode -s`, `grok --resume`). |
| `deja sources` | Discovered stores, sizes, message and redaction counts. |
| `deja mcp` | The stdio MCP server (what `deja install` wires in). |
| `deja warmup` | Build/refresh the index without searching — handy in cron or shell startup. |
| `deja statusline` | One line for your status bar: recalls served to agents today. `deja install statusline` wires it into Claude Code (won't touch an existing statusline). |

Context piping without MCP:

```sh
claude "Prior context: $(deja ctx 'database migration')"
```

## Sync between machines

Point both machines at one shared folder (Syncthing, iCloud, a git repo — anything that moves files):

```sh
deja sync export ~/Sync/deja   # machine A: appends new batches since last export
deja sync import ~/Sync/deja   # machine B: picks up what it hasn't seen
```

Or skip the shared folder when the other machine is a ssh hop away:

```sh
deja sync ssh mini          # push new records to mini and import them there
deja sync ssh mini --pull   # fetch mini's new records into this machine
```

`ssh` mode uses your system ssh/scp and the `deja` binary on the remote (looked up on PATH, falling back to `~/.local/bin/deja`).

Batches are plain JSONL, redacted on the way out. Import is idempotent, so keep the folder as an append-only log and run both commands from cron if you like. Records never echo back to their origin. `--full` re-exports everything regardless of watermarks — useful when adding a machine after old batches are gone. Synced sessions show up under `imported:<project>` in search, `recall`, and session-start auto-recall.

## Teach your agent to remember

`deja install --all` wires up MCP recall (Claude Code, Codex, opencode, Cursor, Gemini CLI, Antigravity — aider has no MCP client, pipe `deja ctx` instead); `deja install --auto` also adds session-start auto-recall on every harness it finds (Claude Code hook, Codex hooks.json, an opencode plugin). To make
the agent reach for memory on its own, add this to your `CLAUDE.md` /
`AGENTS.md`:

```
Before debugging or re-implementing something, run `deja "<query>"` (or the
MCP recall tool) — past agent sessions across Claude Code, Codex, opencode, aider, Gemini CLI, Cursor, Antigravity and Grok Build
are indexed locally. Cite what you reuse.
```

## MCP tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `recall` | `query`, `harness?`, `limit?` | Dense matching snippets, ≤4KB — cheap on context. |
| `recall_context` | `query` | Markdown digest of the best-matching session. |

With `--auto`, a SessionStart hook also feeds the current project's recent memory in automatically — read-only, capped at 2KB, and it never delays or breaks agent startup.

## Security

Subagent transcripts are skipped by default (they mostly duplicate the parent session); set `DEJA_INCLUDE_SUBAGENTS=1` to index them. Files caught mid-write are handled safely — the torn tail line is picked up on the next pass.

Credentials are redacted at index time: AWS keys, generic `api_key=`/`token=` assignments, bearer tokens and raw JWTs, PEM private key blocks, provider tokens (`ghp_`, `sk-`, `npm_`, `xox.`, `AIza`), and `scheme://user:pass@host` URLs. The value is replaced with `[redacted:<kind>]`; surrounding text stays searchable. `deja sources` shows per-store counts. Opt out with `DEJA_NO_REDACT=1` (unsafe). `deja share` and `deja sync export` re-apply redaction on the way out.

## Supported harnesses

| Harness | Store | Status |
| --- | --- | --- |
| Claude Code | `~/.claude/projects/**/*.jsonl` | ✅ |
| Codex CLI | `~/.codex/sessions/**` + `history.jsonl` | ✅ |
| opencode | `~/.local/share/opencode/opencode.db` | ✅ |
| aider | `.aider.chat.history.md` | ✅ |
| Gemini CLI | `~/.gemini/tmp/*/chats/**` | ✅ |
| Cursor | `state.vscdb` + `~/.cursor/projects/**/agent-transcripts/*.jsonl` | ✅ |
| Antigravity | `~/.gemini/antigravity*/brain/*/.system_generated/logs/transcript.jsonl` | ✅ |
| Grok Build | `~/.grok/sessions/**/updates.jsonl` | ✅ |

Custom locations via `DEJA_CLAUDE_ROOT`, `DEJA_CODEX_ROOT`, `DEJA_OPENCODE_DB`, `DEJA_AIDER_ROOTS`, `DEJA_GEMINI_ROOT`, `DEJA_CURSOR_ROOT`, `DEJA_CURSOR_CLI_ROOT`, `DEJA_ANTIGRAVITY_ROOT`, `DEJA_GROK_ROOT`, `DEJA_INDEX_DIR`. Grok's native `GROK_HOME` is also honored.

## Performance

Measured on a real corpus — 1,250+ sessions, ~3.3GB across three harnesses:

| | |
| --- | --- |
| Warm search | **7–9 ms** typical, ~40 ms worst-case |
| Cold index (once) | ~10 s |
| Index size | ~2.4% of corpus |

The index is incremental: when a session file grows, only that file is re-read.

## How it works

Local inverted index in `~/.cache/deja`: parse JSONL/SQLite stores → redact credentials → `records.bin` + token buckets → `manifest.json` tracks per-file state so repeat runs only ingest what changed. The MCP server, stats, share and sync all read the same index. Details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

**Privacy:** no network path exists in the indexing or search code. Local files in, local cache out.

## FAQ

**Does anything leave my machine?** No. There is no network code in the tool. `sync` writes files to a directory you choose; moving them is up to you.

**How is this different from cass?**
[cass](https://github.com/Dicklesworthstone/coding_agent_session_search) is the kitchen-sink take on session search: 22 providers, Rust, optional semantic embeddings, a TUI. deja is the opposite bet — one small Go binary, pure lexical, eight harnesses, zero setup — plus the memory-layer pieces around it: auto-recall, redaction, share, sync.

**And from MemPalace / Mem0 / Letta?**
Those are memory *platforms*: embeddings, vector stores, capture hooks or APIs that record going forward. deja has no capture step at all — it indexes what your agents already wrote to disk, including months of history from before you installed it. They can coexist.

**What about secrets already in my logs?** They stay in the original harness files (that's your agent's data), but they don't enter deja's index, digests, shares or sync exports.

**What about Windows?** Builds exist, CI runs the suite on Windows; macOS/Linux are the battle-tested paths. Field reports welcome: [#9](https://github.com/vshulcz/deja-vu/issues/9).

**Can I exclude a project?** Not yet — planned as `--exclude` ([#8](https://github.com/vshulcz/deja-vu/issues/8)). Today you can point `DEJA_*_ROOT` at a filtered copy.

**How do I wipe everything?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Contributing

`make build test lint` — see [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness is one parser file: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#adding-a-harness). Good first issues are labeled.

## License

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
