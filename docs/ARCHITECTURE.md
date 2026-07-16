# Architecture

This document is for people changing `deja` internals.

## Source parsers

Parsers live in `internal/sources` and return `[]model.Session`.

| Source | Code | Input |
| --- | --- | --- |
| Claude Code | `claude.go` | JSONL files under `~/.claude/projects` |
| Codex CLI | `codex.go` | rollout JSONL files plus `history.jsonl` under `~/.codex` |
| opencode | `opencode.go` | SQLite database at `~/.local/share/opencode/opencode.db` |
| aider | `aider.go` | `.aider.chat.history.md` files under configured project roots |
| Gemini CLI | `gemini.go` | JSON and JSONL chats under `~/.gemini/tmp` |
| Cursor | `cursor.go` | SQLite state stores plus CLI agent transcripts |
| Antigravity | `antigravity.go` | JSONL transcripts under `~/.gemini/antigravity*` |
| Grok Build | `grok.go` | ACP update streams and session summaries under `~/.grok/sessions` |

File-based sources are parsed with a worker pool sized to `runtime.NumCPU()`. Results are collected by input file index and then appended in sorted path order, so parsing can be parallel while index writes stay deterministic.

opencode and Cursor IDE state are read through the local `sqlite3` command. Cursor CLI transcripts are plain JSONL. There is no CGO SQLite dependency.

## Index format

Default path: `~/.cache/deja/index.db`.

Files:

- `records.bin`: length-prefixed records. Each record stores session key, source path, role, text, and timestamp.
- `buckets/*.bin`: token bucket files. A token maps to compact postings: record offset plus session ordinal.
- `manifest.gob` / `sessions.gob`: index version, source file state, redaction counters, sync export watermarks, imported-record dedupe keys, session metadata (including ordinals), build time, and search scope.

## Secret redaction

`internal/redact` runs before every `writeRecord` path: cold rebuild, `writeSessions`, non-append incremental replacement, and append-only incremental ingest. The pass is disabled only when `DEJA_NO_REDACT=1` is set; that escape hatch is unsafe because plaintext credentials will be written to the local index.

The redactor replaces only secret values, keeping keys and surrounding prose searchable. It covers AWS access keys and AWS secret assignments, generic credential assignments, bearer tokens, PEM private key blocks, GitHub/OpenAI/npm/Slack/Google provider prefixes, and connection URLs with `scheme://user:pass@host` credentials.

Redaction counts are accumulated per source file in `FileState.Redactions` and as a manifest total. `deja sources` reads those counters and prints `redacted=` per harness.

Search flow:

1. Tokenize the query.
2. Read posting lists from the token buckets.
3. Intersect posting lists for multi-word searches.
4. Aggregate posting counts per session and pre-rank by count × recency using session metadata.
5. Read matching records from `records.bin` only for the top sessions (`--all` keeps all candidates).
6. Group records back into sessions and rank in `internal/search`.

`--harness`, `--project`, and `--since` are applied during pre-rank from session metadata. `--role` needs record data, so it is applied after the pre-rank cut; pre-rank counts may include other roles.

Regex search scans records because arbitrary regex cannot use token postings safely.

## Sync format

`deja sync export <dir>` reads `records.bin` and writes JSONL batch files named `deja-sync-<source-hash>-<timestamp>.jsonl`. Each line is one object:

```json
{"harness":"claude","session_id":"abc123","project":"api","role":"assistant","text":"fixed by ...","time":"2026-07-14T12:00:00Z"}
```

The export watermark is per source path (falling back to session key for synthetic records) and is stored in `manifest.gob` as the max exported record timestamp. Re-running export emits only records with a newer timestamp for that source. Text is redacted again during export.

`deja sync import <dir>` reads all `*.jsonl` batches, appends records to the local index, updates touched token buckets, and writes imported session metadata with the original harness and an `imported:` project prefix. Imported IDs are namespaced (`imported-<hash>`) so they do not clobber local sessions from the same harness. The manifest stores dedupe keys of `harness:session_id:time`, making re-import idempotent. Imported records live only in the index, so full rebuilds replay them from the old `records.bin` before regenerating from sources, and exports skip them to avoid echoing history back to its origin.

`deja sync ssh <host>` wraps the same export/import in one command: export to a temp dir, scp the batches, run the remote import (system ssh/scp, remote binary from PATH or `~/.local/bin/deja`).

## Incremental algorithm

`currentFiles` records path, size, and mtime for known stores.

`EnsureForSearch` compares the current file set with `manifest.json`:

- fresh manifest: do nothing;
- version or scope mismatch: rebuild;
- append-only JSONL/opencode changes: append new records and update touched buckets;
- removed files or non-append changes: rewrite the index while preserving unchanged records and replacing changed sessions.

Cold rebuild does all parsing first, then writes `records.bin`, buckets, and manifest from one goroutine. That keeps the on-disk index coherent and avoids concurrent writers.

## MCP server design

`cmd/deja/mcp.go` implements a small JSON-RPC stdio server.

Supported methods:

- `initialize`
- `tools/list`
- `tools/call`

Tools:

- `recall`: returns compact snippets for matching sessions.
- `recall_context`: returns the markdown digest used by `deja ctx`.

The MCP server calls the same index/search code as the CLI. It writes protocol responses to stdout and keeps logs/progress off stdout so agents receive valid JSON-RPC.

## Claude SessionStart hook

`deja install --auto` installs the Claude MCP entry and adds a matcher-less command hook to `~/.claude/settings.json`:

```json
{"type":"command","command":"/abs/path/to/deja hook-context"}
```

`deja hook-context` is intentionally hidden from normal help. It derives the current project from `CLAUDE_PROJECT_DIR` or `cwd` using the same Claude project-name logic as the parser, reads only an existing warm index (`manifest.gob`/`sessions.gob` must already exist), selects the most recent matching sessions by metadata project, and prints Claude's `SessionStart` response JSON with a compact markdown digest capped at 2KB. It never triggers a cold index build; missing index, empty results, corrupt data, or any other error produce no output and exit 0 so agent startup is not blocked.

## Add a new harness

Implement the same shape as the existing sources.

Interface:

```go
func LoadNewHarness() []model.Session
func ParseNewHarnessFile(path string) ([]model.Session, error)
func ParseNewHarnessFileFromOffset(path string, offset int64) ([]model.Session, error) // if append-only
```

Five steps:

1. Add parser code in `internal/sources` that returns `model.Session` with stable `Harness`, `ID`, `Project`, `Path`, `Started`, `Updated`, and `Messages`.
2. Add file discovery to `currentFiles` and harness detection to `harnessForPath` in `internal/index/index.go`.
3. Add load and incremental parse paths in `load`, `parseChangedFile`, and `parseAppendedFile`.
4. Add install/uninstall config handling in `cmd/deja/install.go` if the harness supports MCP.
5. Add fixtures and tests for parsing, indexing, search, and install behavior.
