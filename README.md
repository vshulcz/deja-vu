<p align="center"><img src="assets/logo.svg" width="340" alt="deja-vu"></p>

<p align="center"><strong>Persistent memory for your coding agents.</strong></p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/vshulcz/deja-vu"><img src="https://goreportcard.com/badge/github.com/vshulcz/deja-vu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><img src="assets/demo.gif" alt="deja demo"></p>

Coding agents forget everything between sessions. The solutions are still on your disk — Claude Code, Codex and opencode write every conversation to local files, gigabytes of debugged problems and design decisions you can't search.

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
| `deja last [n]` | Recent sessions across all harnesses. |
| `deja sources` | What stores were found, sizes, message counts. |
| `deja mcp` | Run the stdio MCP server (what `deja install` wires in). |

Context piping without MCP:

```sh
claude "Prior context: $(deja ctx 'database migration')"
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
| Warm search | **45–97 ms** |
| Cold index (once) | ~10 s |
| Index size | ~2.4% of corpus |

The index is incremental: when a session file grows, only that file is re-read.

## How it works

Local inverted index in `~/.cache/deja`: parse JSONL/SQLite stores → `records.bin` + token buckets → `manifest.json` tracks per-file size/mtime so repeat runs only ingest what changed. The MCP server is the same index behind two tools. Details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

**Privacy:** no network path exists in the indexing or search code. Local files in, local cache out.

## FAQ

**Does anything leave my machine?** No. There is no network code in the tool.

**How is this different from `/resume` or a history viewer?** Those are per-harness and per-project. `deja` is one index across every harness and project on the machine, plus an MCP tool so the *agent* can search it.

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
