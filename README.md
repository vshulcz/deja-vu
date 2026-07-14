<p align="center"><img src="assets/logo.svg" width="340" alt="deja-vu"></p>

<p align="center"><strong>Persistent memory for your coding agents.</strong></p>

<p align=center>
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://pkg.go.dev/github.com/vshulcz/deja-vu"><img src="https://pkg.go.dev/badge/github.com/vshulcz/deja-vu.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/vshulcz/deja-vu"><img src="https://goreportcard.com/badge/github.com/vshulcz/deja-vu" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<!-- Go Report Card and pkg.go.dev badges populate after the repository is public and indexed. -->

<p align="center"><img src="assets/demo.gif" alt="deja demo"></p>

# deja-vu

## Why

Agents forget everything between sessions.
Your machine already holds every solution they found.
`deja` indexes those histories and serves them back — to you and to the agent.

## Install

Curl installer:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Go install:

```sh
go install github.com/vshulcz/deja-vu/cmd/deja@latest
```

Homebrew (coming with the tap):

```sh
brew install vshulcz/homebrew-tap/deja-vu
```

Then install the MCP integration you use:

```sh
deja install --all
```

Then ask your agent:

> Use deja to recall how we fixed the migration rollback test.

Manual CLI use:

```sh
deja "incremental indexing bug"
```

## Example output

```text
$ deja --harness claude --since 30d "jwt refresh token"
[claude · api · 2026-07-08 · 2 matches · 8f31c0a9]
user: login started failing after refresh token rotation; jwt kid mismatch in tests
assistant: fixed by reloading jwks cache after rotateKey and adding a clock-skew test

[claude · web · 2026-07-01 · 1 match · b77d91e2]
assistant: refresh token cookie needed SameSite=Lax in local callback flow
```

## MCP tools

`deja mcp` runs a stdio MCP server for agent integrations.

| Tool | Arguments | Output |
| --- | --- | --- |
| `recall` | `query`, optional `harness`, optional `limit` | Dense matching snippets, capped around 4KB by default. |
| `recall_context` | `query` | Markdown context for the best match, same shape as `deja ctx`. |

Install targets are idempotent and create one backup before editing config:

```sh
deja install claude-code
deja install codex
deja install opencode
deja install --all
```

## CLI reference

| Command | Flags / args | Notes |
| --- | --- | --- |
| `deja <query>` | `--json`, `--re`, `--rebuild`, `--harness <name>`, `--project <text>`, `--since <duration>`, `--role <role>` | Search local history. Durations accept Go durations and `30d`. |
| `deja ctx <query\|id-prefix>` | none | Print a compact markdown digest for agent context. |
| `deja show <id-prefix>` | none | Print one matching session. |
| `deja last [n]` | optional count | List recent sessions. Default `10`. |
| `deja sources` | none | Print discovered stores, counts, and sizes. |
| `deja mcp` | none | Run the stdio MCP server. |
| `deja install <target>` | `claude-code`, `codex`, `opencode`, `--all` | Add the MCP entry to local agent config. |
| `deja uninstall <target>` | `claude-code`, `codex`, `opencode`, `--all` | Remove the MCP entry. |
| `deja version` | `--version`, `-version` | Print the build version. Set with `-ldflags "-X main.version=vX.Y.Z"`. |

## Harnesses

| Harness | Store | Status | Install target |
| --- | --- | --- | --- |
| Claude Code | `~/.claude/projects/<project-dir>/**/*.jsonl`, including `subagents/*.jsonl` | supported | `deja install claude-code` |
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` and `~/.codex/history.jsonl` | supported | `deja install codex` |
| opencode | `~/.local/share/opencode/opencode.db` | supported | `deja install opencode` |
| aider | local chat history | planned | — |
| gemini | local chat history | planned | — |

Environment overrides for tests or custom stores:

```sh
DEJA_CLAUDE_ROOT=/path/to/claude/projects
DEJA_CODEX_ROOT=/path/to/codex
DEJA_OPENCODE_DB=/path/to/opencode.db
DEJA_INDEX_DIR=/path/to/deja/index.db
```

## Performance

Measured on a local mixed corpus of about 3.3GB:

| Case | Result |
| --- | --- |
| Cold index | ~10s once |
| Warm search | 45-97ms |
| Index size | ~2.4% of corpus |

The index is incremental. Appending to one JSONL session updates that source file instead of rebuilding the corpus.

## How it works

1. Discover Claude, Codex, and opencode local history stores.
2. Parse source files into sessions and messages.
3. Write `records.bin` plus token bucket files under `~/.cache/deja/index.db`.
4. Track file size, mtime, and session metadata in `manifest.json`.
5. Serve searches through the CLI or the MCP stdio server.

Developer details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Privacy

All local. Nothing leaves your machine.

`deja` reads local history files and writes a local cache only.

## FAQ

**Does it send data anywhere?**  
No. It has no network path for indexing or search.

**How big is the index?**  
About 2.4% of the source corpus in current measurements.

**What about Windows?**  
The code has Windows file locking, but the main tested path is macOS/Linux for now.

**How do I exclude a project?**  
Move or delete that history from the agent's local store before indexing. A first-class exclude file is not built yet.

**How do I wipe it?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```
