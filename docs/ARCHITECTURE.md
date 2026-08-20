# Architecture

This document is for people changing `deja` internals.

## Source parsers

Parsers live in `internal/sources` and return `[]model.Session`. The table is
the nineteen the loader registers; `docs/registry/` describes each store's
layout in detail, and `internal/sources/registry_test.go` checks that index
against the loader list.

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
| Qwen Code | `qwen.go` | JSONL chats under `~/.qwen/projects/*/chats` |
| pi | `pi.go` | JSONL transcripts under `~/.pi/agent/sessions` |
| Copilot CLI | `copilot.go` | `events.jsonl` per session under `~/.copilot/session-state` |
| Cline | `cline.go` | task JSON under the VS Code extension's storage, both store generations |
| Roo Code | `roo.go` | task JSON under `rooveterinaryinc.roo-cline` in VS Code globalStorage |
| Goose | `goose.go` | legacy JSONL sessions and the newer SQLite session store |
| Kimi Code | `kimi.go` | per-agent `wire.jsonl` under `~/.kimi-code/sessions` |
| OpenClaw | `openclaw.go` | append-only pi-format JSONL under `~/.openclaw/agents` |
| Hermes | `hermes.go`, `hermes_pg.go` | SQLite state per profile, or Postgres when `DEJA_HERMES_PG_DSN` is set |
| deja notes | `notes.go` | `deja remember` entries in `notes.jsonl` |

File-based sources are parsed with a worker pool sized to `runtime.NumCPU()`. Results are collected by input file index and then appended in sorted path order, so parsing can be parallel while index writes stay deterministic.

opencode and Cursor IDE state are read through the local `sqlite3` command. Cursor CLI transcripts are plain JSONL. There is no CGO SQLite dependency.

## Index format

Default path: `~/.cache/deja/index.db`.

Files:

- `records.bin`: length-prefixed records. Each record stores session key, source path, role, text, and timestamp.
- `buckets/*.bin`: token bucket files. A token maps to compact postings: record offset, session ordinal, and one bit marking the posting as a work record.
- `manifest.gob` / `sessions.gob`: index version, source file state, redaction counters, sync export watermarks, imported-record dedupe keys, session metadata (including ordinals, the files a session touched most, and hashes of the questions it asked), build time, and search scope.

### Roles

Beyond `user`, `assistant` and `developer`, records carry what the agent did:

| role | holds |
| --- | --- |
| `tool-output` | what a tool printed. Claude files this under the user role in its own transcripts, which is why it used to arrive labelled as something a person said |
| `files` | the paths a turn opened or edited |
| `command` | a shell command worth keeping — an allowlist of build, test, VCS and deployment tooling, single-line only |
| `edit` | a span an edit replaced: the path on the first line, the exact removed bytes after it. Only the path earns postings, since nothing searches the body |

These are indexed and searchable by `--role`, and served in ordinary results only when asked for by role: a path that happens to contain the words of a question is not an answer to it. The postings carry a bit for them so the per-session read bound can spend its budget on speech first.

## Notes source

Explicit notes are stored as one JSON object per line in
`~/.local/share/deja/notes.jsonl`, or under `XDG_DATA_HOME`; `DEJA_NOTES_FILE`
overrides the path. Each record contains an RFC3339 `ts`, `project`, and
`text`. Notes are grouped into one user-message session per project and
calendar day in the local zone of whichever run indexed them, then redacted and
indexed like every other source. `deja index` regroups buckets minted in
another zone. The file is primary data; the index remains a rebuildable cache.

## Secret redaction

`internal/redact` runs before every `writeRecord` path: cold rebuild, `writeSessions`, non-append incremental replacement, and append-only incremental ingest. The pass is disabled only when `DEJA_NO_REDACT=1` is set; that escape hatch is unsafe because plaintext credentials will be written to the local index.

The redactor replaces only secret values, keeping keys and surrounding prose searchable. It covers AWS access keys and AWS secret assignments, generic credential assignments, bearer tokens, PEM private key blocks, GitHub/OpenAI/npm/Slack/Google provider prefixes, and connection URLs with `scheme://user:pass@host` credentials.

Redaction counts are accumulated per source file in `FileState.Redactions` and as a manifest total. `deja sources` reads those counters and prints `redacted=` per harness.

Search flow:

1. Tokenize the query.
2. Read posting lists from the token buckets.
3. Intersect posting lists for multi-word searches.
4. Filter postings by session metadata and read every matching candidate record.
5. Group records back into sessions and score them in `internal/search` with BM25
   (`k1=1.2`, `b=0.75`). Document frequency and document length are measured
   over the candidate records at search time. User-message term contributions
   receive a 1.3 multiplier, and the score is multiplied by `1/(1+age_days)`.
6. Sort by score, then updated time descending, then session ID ascending; the
   normal result limit is applied after ranking.

`--harness`, `--project`, and `--since` are applied from session metadata before
scoring. `--role` is applied while reading candidate records.

Regex search scans records because arbitrary regex cannot use token postings safely.

`deja blame` retrieves candidates using the basename stem through the existing token
postings, then verifies the basename as a path component or standalone word in the
candidate text. Full and longer suffix path mentions outrank bare basenames; mention
counts are blended with recency and an absolute project-root match receives a boost.

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

`EnsureForSearch` compares the current file set with `manifest.gob`:

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

- `recall`: compact snippets for matching sessions.
- `recall_context`: the markdown digest `deja ctx` prints.
- `blame`: the sessions that discussed a file, and what was decided.
- `fix`: what this machine ran after the same error last time.
- `how`: the real invocation for a tool here, from what agents ran.
- `remember`: stores one durable decision for later recall.

Each carries an annotation naming what it is for, so an agent can choose between
them without reading the descriptions in full.

The MCP server calls the same index/search code as the CLI. It writes protocol responses to stdout and keeps logs/progress off stdout so agents receive valid JSON-RPC.

## Claude SessionStart hook

`deja install --auto` installs the Claude MCP entry and adds a matcher-less command hook to `~/.claude/settings.json`:

```json
{"type":"command","command":"/abs/path/to/deja hook-context"}
```

`deja hook-context` is intentionally hidden from normal help. It derives the current project from `CLAUDE_PROJECT_DIR` or `cwd` using the same Claude project-name logic as the parser, reads only an existing warm index (`manifest.gob`/`sessions.gob` must already exist), selects the most recent matching sessions by metadata project (ranked by the files the working tree is touching), leads them with the project's `accepted` promoted notes, and prints Claude's `SessionStart` response JSON with a compact markdown digest capped at 2KB. It never triggers a cold index build; missing index, empty results, corrupt data, or any other error produce no output and exit 0 so agent startup is not blocked.

`--auto` wires three more hooks with the same fail-open contract, each a hidden subcommand that reads only a warm index and exits 0 on anything unexpected:

- **`hook-prompt`** (`UserPromptSubmit`) searches the index for the prompt's content and injects a small digest, or the `you have been here` line on a déjà-vu match.
- **`hook-tool`** (`PreToolUse`, matched to the editing/command tools) reads the tool payload — a `Bash` command or an `Edit`/`Write`/`apply_patch` target — and injects one line naming that file's or command's prior decision. Deliberately thin: it fires once per action, so it dedupes per agent session, stays silent unless the history is a pattern, and carries at most one decision.
- **`hook-precompact`** (`PreCompact`, Claude Code) captures the current transcript into the index before compaction discards it.

## Ranking

Search intersects postings to find candidates, then scores each with BM25 over the query tokens and multiplies in a few bounded signals — each able to break a tie, none able to outrank plain relevance:

- **decision** — a session that reached a conclusion is lifted, and the decision-carrying line (not the query-match line) is what recall shows.
- **outcome** — a session whose own text reports it reverted an approach and settled nothing else is damped; one that reverted and then settled keeps the decision lift. A promoted `rejected`/`superseded`/`stale` lifecycle state travels with the session and is surfaced on every hit.
- **worn (reuse)** — a session agents keep recalling is lifted on a `log2` curve capped at +50%, on the theory that what the machine keeps needing is worth surfacing; the cap keeps popularity below relevance.
- **promoted note** — a curated note outranks the raw transcript it was distilled from.
- **freshness** — recency decays the score gently so time is a hint, not a filter.

`recall_context` and the hooks reuse the same scored order; the point-of-action decision line comes from the same conclusion extraction the digest uses.

## Semantic sidecar

`deja embed` writes `<index-dir>.vectors.bin`. The file begins with `DJV1`, a
version, vector dimension, model name, manifest generation, vector count, and
covered-record watermark. Each entry stores the records.bin byte offset, its
session key, and fixed-width float32 values. Writes use a temporary file and
rename. A changed manifest generation or model discards old entries; a corrupt
sidecar is treated as absent and rebuilt.

### Hybrid reranking

Search first produces lexical BM25 results. When a matching sidecar exists, up
to 64 candidates are reranked using the query vector. The final score is
`0.5 * normalized lexical score + 0.5 * cosine similarity`. A failed query
embedding prints one notice and returns the original lexical order.

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
