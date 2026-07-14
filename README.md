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

Claude Code, Codex and opencode write every conversation to local files — gigabytes of debugged problems and design decisions you can't search. deja is a zero-dependency binary that indexes those histories, retroactively, in about ten seconds.

`deja` indexes those histories and serves them back:

- **to your agent** — an MCP `recall` tool, so the agent can answer *"we fixed this three weeks ago — worker.py:87 leaked sessions on cancel"* instead of re-debugging it;
- **to you** — a fast CLI: `deja "connection pool exhausted"`.

One binary, no dependencies, everything stays on your machine.

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
deja install --all        # or: claude-code | codex | opencode
```

## Auto-recall (optional)

For Claude Code, `deja install --auto` does the normal MCP install and also adds a read-only `SessionStart` hook. When a session starts, Claude receives a tiny (<2KB) markdown digest of the most recent sessions for the current project, so it can remember prior fixes without being asked; if the local index is missing or stale, the hook prints nothing and startup continues.

```sh
deja install --auto
```

That's it. Next session, ask your agent:

> have we dealt with jwt refresh rotation before? check your memory

The agent calls `recall` and gets back the sessions where you solved it — including ones from a *different* harness.

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
| `deja <query>` | Search all histories. Multi-word = AND. `--re` for regex, `--harness`, `--project`, `--since 30d`, `--role`, `--json`. |
| `deja ctx <query>` | Compact markdown digest of the best match — pipe it into a prompt. |
| `deja show <id>` | Print one session, tool noise collapsed. |
| `deja share <id>` | Sanitized markdown digest for a colleague: project/harness/date, user problem, key assistant conclusions/code. |
| `deja sync export <dir>` / `deja sync import <dir>` | Append-only JSONL batches for moving indexed memory between machines. |
| `deja last [n]` | Recent sessions across all harnesses. |
| `deja sources` | What stores were found, sizes, message counts. |
| `deja stats [--json]` | Shareable summary: totals, harness split, top projects, activity sparkline, longest session, busiest day. |
| `deja mcp` | Run the stdio MCP server (what `deja install` wires in). |

Context piping without MCP:

```sh
claude "Prior context: $(deja ctx 'database migration')"
```

## Share

`deja share <id-prefix>` prints a pasteable markdown digest of one session. It is more complete than `ctx`, skips tool-wrapper noise, and every output line is redacted again before printing.

```sh
deja last 5
deja share 8f31c0a9 > session-digest.md
```

## Sync

`deja sync` moves indexed, redacted records between machines with append-only JSONL batches. Export tracks per-source watermarks in the index manifest, so repeated exports only write new records. Import marks sessions as imported and dedupes by `harness:id:time`, so re-importing the same batch is safe.

Laptop → server workflow:

```sh
# laptop
deja sync export ~/deja-batches
rsync -a ~/deja-batches/ server:~/deja-batches/

# server
deja sync import ~/deja-batches
deja "jwt refresh token"
```

## Stats

`deja stats` renders a screenshot-ready summary entirely from the local index. Use `--json` for the same numbers in machine-readable form.

```text
$ deja stats
deja stats
indexed agent work, wrapped for sharing

Sessions  1284
Messages  58391
Range     2025-08-03 → 2026-07-14

By harness
  [claude]              884 sessions  42110 messages
  [codex]               291 sessions  11142 messages
  [opencode]            109 sessions   5139 messages

Top projects
  deja/vu            ################## 312
  api                ############ 211
  web                ######### 164

Last 12 months
  ▁▁▂▃▄▅▆▇██▇█  Aug Sep Oct Nov Dec Jan Feb Mar Apr May Jun Jul

Highlights
  Longest session  642 messages · [claude] · index append race in records.bin
  Busiest day      2026-07-08 · 1831 messages
```

## MCP tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `recall` | `query`, `harness?`, `limit?` | Dense matching snippets, ≤4KB — cheap on context. |
| `recall_context` | `query` | Markdown digest of the best-matching session. |

## Supported harnesses

| Harness | Store | Status |
| --- | --- | --- |
| Claude Code | `~/.claude/projects/**/*.jsonl` | ✅ |
| Codex CLI | `~/.codex/sessions/**` + `history.jsonl` | ✅ |
| opencode | `~/.local/share/opencode/opencode.db` | ✅ |
| aider, Gemini CLI | — | planned |

Custom locations via `DEJA_CLAUDE_ROOT`, `DEJA_CODEX_ROOT`, `DEJA_OPENCODE_DB`, `DEJA_INDEX_DIR`.

## Performance

Measured on a real corpus — 1,250+ sessions, ~3.3GB across three harnesses:

| | |
| --- | --- |
| Warm search | **7–9 ms** typical, ~40 ms worst-case |
| Cold index (once) | ~10 s |
| Index size | ~2.4% of corpus |

The index is incremental: when a session file grows, only that file is re-read.

## How it works

Local inverted index in `~/.cache/deja`: parse JSONL/SQLite stores → redact secrets → `records.bin` + token buckets → manifest tracks per-file size/mtime so repeat runs only ingest what changed. The MCP server is the same index behind two tools. Details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

**Privacy:** no network path exists in the indexing or search code. Local files in, local cache out.

## Security

deja redacts secrets at ingest before writing `~/.cache/deja/index.db/records.bin`. It keeps surrounding text searchable and replaces only the secret value with `[redacted:<kind>]`.

Redacted classes: AWS access keys and AWS secret assignments, generic `api_key`/`secret`/`token`/`passwd`/`password`/`authorization` assignments with long base64/hex-ish values, bearer tokens, PEM private key blocks, GitHub/OpenAI/npm/Slack/Google provider tokens, and connection URLs with `user:pass@host` credentials.

`deja sources` reports a `redacted=` count from the manifest. Unsafe escape hatch: set `DEJA_NO_REDACT=1` to disable ingest redaction for users who intentionally want plaintext secrets in the local index.

## FAQ

**Does anything leave my machine?** No. There is no network code in the tool.

**Are secrets stored in the index?** By default, no for supported patterns: secrets are redacted before index writes. If older indexes predate redaction, v0.2.0 bumps the index version and rebuilds transparently. `DEJA_NO_REDACT=1` disables this and is unsafe.

**What does the auto-recall hook do?** It is read-only: it only checks the warm local index for the current Claude project and returns a capped (<2KB) summary. It never builds the index from the hook, and on any error it exits successfully with no output.

**How is this different from `/resume` or a history viewer?** Those are per-harness and per-project. `deja` is one index across every harness and project on the machine, plus an MCP tool so the *agent* can search it.

**How is this different from cass?**
[cass](https://github.com/Dicklesworthstone/coding_agent_session_search) is the kitchen-sink take on the same idea: 22 providers, Rust, optional semantic embeddings, a TUI. deja is the opposite bet — one small Go binary, pure lexical, the three harnesses I actually run, zero things to configure or download. If you want hybrid search over everything, use cass. If you want grep-fast recall that installs in ten seconds, that's deja.

**And from MemPalace / Mem0 / Letta?**
Those are memory *platforms*: embeddings, vector stores, capture hooks or APIs that record sessions going forward. deja has no capture step at all — it indexes what your agents already wrote to disk, including months of history from before you installed it. They can coexist.

**What about Windows?** Builds exist and file locking is implemented; macOS/Linux are the tested paths today.

**Can I exclude a project?** Not yet — planned as `--exclude`. Today you can point `DEJA_*_ROOT` at a filtered copy.

**How do I wipe everything?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Contributing

`make build test lint` — see [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness is one parser file: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#adding-a-harness).

## License

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
