<p align="center"><img src="assets/logo.svg" width="340" alt="deja-vu"></p>

<p align="center"><strong>Your agents already solved this. deja finds it.</strong><br>Memory tools start empty and record forward. deja starts full: it indexes the sessions your coding agents already wrote to disk &mdash; months of history from before you installed it &mdash; searches 3.3&nbsp;GB in ~12&nbsp;ms, serves it back to any agent over MCP, and moves with you between machines over SSH. One zero-dependency binary, fully local.</p>

<p align="center"><strong>84.9% hit@1</strong> on LongMemEval-S retrieval &mdash; no LLM, no embeddings, no API key. <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Harness in-repo, run it yourself.</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">vshulcz.github.io/deja-vu</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">how deja compares</a></p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://www.npmjs.com/package/@vshulcz/deja-vu"><img src="https://img.shields.io/npm/v/%40vshulcz%2Fdeja-vu?label=npm" alt="npm"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/vshulcz/deja-vu"><img src="https://api.scorecard.dev/projects/github.com/vshulcz/deja-vu/badge" alt="OpenSSF Scorecard"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><img src="assets/demo.gif" alt="deja demo"></p>

Claude Code, Codex, opencode, aider, Gemini CLI, Cursor, Antigravity, Grok Build, Qwen Code, Kimi Code, Cline, Roo Code, OpenClaw, pi and Copilot CLI write every conversation to local files — gigabytes of debugged problems and design decisions you can't search. deja is a zero-dependency binary that turns those histories into a memory layer:

| Feature | What it does |
| --- | --- |
| **Search** | `deja "connection pool exhausted"` — ~12 ms over gigabytes, retroactive: months of logs from before you installed it; natural-language questions fall back to a relevance tier — 84.9% hit@1 on LongMemEval-S session retrieval, harness in-repo |
| **Agent recall** | MCP `recall` tool — the agent answers *"we fixed this three weeks ago"* instead of re-debugging, across harnesses: solve it in Codex, Claude remembers |
| **Sync** | `deja sync ssh laptop` — your memory follows you between machines, append-only, idempotent, no cloud in the middle |
| **Handoff** | `deja handoff --to codex` — stuck in one agent? package the live context and continue in another: `codex "$(deja handoff --to codex)"` |
| **Auto-recall** | `install --auto` adds a SessionStart hook: relevant memory lands in context before you ask — ranked by the files your repo is touching, ~120 ms on a 1000-session index; Claude Code also captures the current transcript before compaction |
| **Déjà vu moments** | When a prompt matches work your history already answered, deja announces it — *you have been here* — with the session and its age, and counts the moment in `deja stats` |
| **Redaction** | API keys, JWTs, private keys are stripped at index time — the cache is safe to keep |
| **Stats** | `deja stats` — your agent work, wrapped; `--impact` reports only counted numbers: recalls served, session starts that began with memory, served-vs-raw ratio |
| **Promote** | `deja promote <id>` — distill a session into a curated note with provenance, `--tag` keywords and a lifecycle state (accepted / rejected / superseded / stale); notes outrank raw transcripts, and promoting over an existing accepted note surfaces the conflict |
| **Trust scopes** | `policy.json` decides what memory activates where: search / MCP / auto × local / imported / per-peer; receipts and `deja log` name the rule |
| **Deep verify** | `deja doctor --deep` proves the index against the sources — re-parses a sample, resolves postings, separates staleness from drift |
| **Share** | `deja share <id>` — hand a colleague a sanitized digest of a session, secrets already scrubbed |
| **Remember** | `deja remember "text"` or MCP `remember` — keep durable decisions and conclusions |
| **Blame** | `deja blame <path>` — which sessions touched this file, what was decided and why |
| **Semantic** | optional: point `deja embed` at a local Ollama/LM Studio and rephrased queries still hit |

## Privacy

`deja forget` removes matching sessions from a rebuilt index and records exact
session tombstones so a later `deja index` cannot restore them from source
history. Tombstones are stored at `~/.config/deja/tombstones` (or
`$XDG_CONFIG_HOME/deja/tombstones`); use `--dry-run`, `--list`, or `--unforget`.
Ingest exclusions are one case-insensitive project pattern per line in
`~/.config/deja/exclude` (XDG-aware), or comma-separated in
`DEJA_EXCLUDE_PROJECTS`. `deja stats --redaction` reports redactions by
harness and rule, along with tombstone and semantic-sidecar facts.

One binary. No models to download, no services to run, nothing leaves your machine unless you sync or share it. (opencode and Cursor IDE indexing shell out to the `sqlite3` CLI, preinstalled on macOS and most Linux distros; Cursor CLI transcripts do not need it.)

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

### Shell completion

```sh
deja completion bash >> ~/.bashrc
deja completion zsh >> ~/.zshrc
deja completion fish > ~/.config/fish/completions/deja.fish
```

Wire it into the agents you use (edits config, keeps a `.bak`):

```sh
deja install --all     # MCP recall for every agent it finds on this machine
deja install --auto    # same, plus session-start auto-recall where supported
```

On Windows, register the MCP server through the shell wrapper most stdio servers need there: `cmd /c deja mcp` (deja install writes this form automatically; use it if you wire configs by hand).

Install also writes user-level guidance for the harnesses it detects: Claude Code, Codex, Gemini CLI, Qwen, Copilot, and OpenCode use their corresponding guidance files (or the configured `XDG_CONFIG_HOME`). Re-run rewrites deja's skill or marked block without changing surrounding user content. Use `deja install --all --no-guidance` to opt out; Cursor and Grok have no documented user-level guidance location and are skipped.

Install reports whether it found local history and builds the first index immediately when history is present.

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
| `deja <query>` | Search all histories. Multi-word = AND, common English filler words are ignored, substrings match (`code` finds `opencode`), and double-quoted phrases require contiguous text; zero-result queries try word forms before close spellings. `--re`, `--harness`, `--project`, `--since 30d`, `--role`, `--json`. |
| `deja ctx <query>` | Compact markdown digest of the best match — pipe it into a prompt. |
| `deja blame <path>` | Find sessions that discussed a file, newest and most specific first. `--json`, `--all`, and the usual filters are supported. |
| `deja share <id>` | Sanitized session digest for a colleague: secrets redacted, tool noise stripped. |
| `deja stats` | Headline counts, totals, per-harness split, top projects, monthly sparkline. `--json` too. |
| `deja doctor [--json]` | Self-diagnosis: store parse state, sqlite3 presence, MCP wiring per agent, index health, version; `--json` emits the same checks for scripts. |
| `deja sync export <dir> [--full]` / `import <dir>` / `ssh <host> [--pull]` | Move memory between machines — via a shared folder or one ssh command. Watermarked, append-only, idempotent. |
| `deja show <id>` / `deja last [n]` | Read one session / list recent ones (`--project`, `--harness`, `--since`, `--role`). |
| `deja resume <id> [--exec]` | Reopen a found session in its native harness (`claude --resume`, `codex resume`, `opencode -s`, `grok --resume`). |
| `deja sources` | Discovered stores, sizes, message and redaction counts. |
| `deja mcp` | The stdio MCP server (what `deja install` wires in). |
| `deja remember "text" [--project name]` | Store an explicit fact in the notes source. |
| `deja warmup` | Build/refresh the index without searching — handy in cron or shell startup. |
| `deja index [--rebuild]` | Same as warmup; `--rebuild` forces a full rebuild. Cold builds narrate per-harness progress. |
| `deja update` | Download the latest GitHub release, verify its checksum, and replace the current binary. |
| `deja statusline` | One line for your status bar: recalls served to agents today. `deja install statusline` wires it into Claude Code (won't touch an existing statusline). |
| `deja log [n] [--last] [--json]` | Audit what deja actually served: recent recalls and injections, or the exact text of the last injected digest with `--last`. |
| `deja` | On a terminal with an index: a living brief — today's sessions, recalls served, déjà vu moments, and a search suggestion from your own history. |

Stemmed JSON searches set `"stemmed": true` and include the catalog variants used.

Search hits carry `exact`, `close`, or `semantic` confidence tiers; close hits include the matched variant and semantic hits include cosine.

### Share your stats

Run `deja stats --card` to write a self-contained `deja-stats.svg` for a README or profile. The command prints an embed snippet; commit the SVG to your profile or repository if you want it there.

Run `deja stats --html` to write a self-contained, browsable `deja-stats.html` timeline. The HTML export embeds metadata only: dates, harnesses, projects, message counts, and already-redacted first-user titles; it never includes message text.

`deja update` is for standalone installs. Homebrew and npm installs update through the package manager.

### Doctor JSON

`deja doctor --json` reports an explicit state for every check and exits 0 even when it finds a problem. Store states are `ok`, `missing`, `empty`, `unreadable`, or `parsed-zero`; the last state means session files exist but the newest file produced no sessions.

```json
{
  "stores": [{"name": "claude", "state": "ok", "paths": ["/home/me/.claude/projects"], "files": 42}],
  "index": {"state": "stale", "path": "/home/me/.cache/deja/index.db"},
  "mcp": [{"name": "claude-code", "state": "wired", "path": "/home/me/.claude.json"}],
  "sqlite3": {"state": "ok"},
  "version": {"state": "ok", "current": "1.2.3", "latest": "1.2.3"}
}
```

Index states are `ok`, `missing`, or `stale`; MCP states are `wired`, `not-wired`, or `config-missing`. The sqlite3 state is `ok` or `missing`. Version state is `ok`, `update-available`, `ahead`, `dev`, or `unknown`.

Context piping without MCP:

```sh
claude "Prior context: $(deja ctx 'database migration')"
```

Before changing a file, inspect its history:

```sh
deja blame cmd/deja/main.go
```

## Semantic recall (optional)

Semantic search is an opt-in layer for a local Ollama, LM Studio, or
OpenAI-compatible embedding endpoint. Set `DEJA_EMBED_URL` and optionally
`DEJA_EMBED_MODEL`, then run `deja embed`. Ollama defaults to
`nomic-embed-text`; without a configured and reachable runtime, ordinary
lexical search and MCP recall continue unchanged. `--no-embed` or
`DEJA_EMBED=off` disables reranking for one invocation.
Without an embedding endpoint, the semantic zero-result fallback does not exist.

The vector sidecar is stored beside the index as `.vectors.bin`, not in
`index.db`. Float32 vectors cost roughly 4 KB per 1k messages for a 1,024
dimension model, plus a small record key. Embedding is local and can consume
CPU, memory, and model-server time; it never sends raw source files, only the
redacted indexed text truncated to about 2k characters.

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

`deja install --all` wires up MCP recall (Claude Code, Codex, opencode, Cursor, Gemini CLI, Antigravity, Grok Build, Qwen Code, Kimi Code, Cline, OpenClaw, Copilot CLI, pi — aider has no MCP client, pipe `deja ctx` instead); `deja install --auto` does the same and adds session-start auto-recall where the harness supports it (Claude Code hook, Codex hooks.json, an opencode plugin — Cursor, Gemini CLI, Antigravity, Grok Build, Qwen Code, Kimi Code, Cline, OpenClaw, Copilot CLI and pi have no hook that can inject context, so MCP is their full install). To make
the agent reach for memory on its own, add this to your `CLAUDE.md` /
`AGENTS.md`:

```
Before debugging or re-implementing something, run `deja "<query>"` (or the
 MCP recall tool) — past agent sessions across Claude Code, Codex, opencode, aider, Gemini CLI, Cursor, Antigravity, Grok Build, Qwen Code, Kimi Code, Cline, OpenClaw, Copilot CLI and pi
are indexed locally. Cite what you reuse.
```

## MCP tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `recall` | `query`, `harness?`, `limit?` | Dense matching snippets, ≤4KB — cheap on context. |
| `recall_context` | `query`, `harness?` | Markdown digest of the best-matching session. |
| `blame` | `path`, `harness?`, `project?`, `since?`, `limit?` | Sessions that discussed a file, with titles and matched context. |
| `remember` | `text`, `project?` | Stores a durable decision or conclusion for later recall. |

With `--auto`, a SessionStart hook also feeds the current project's recent memory in automatically — read-only, capped at 2KB, and it never delays or breaks agent startup. Because SessionStart also fires after every context compaction, the same memory is re-injected right after Claude Code compacts — and a PreCompact hook captures the transcript into the index beforehand.

## Security

Subagent transcripts are skipped by default (they mostly duplicate the parent session); set `DEJA_INCLUDE_SUBAGENTS=1` to index them. Files caught mid-write are handled safely — the torn tail line is picked up on the next pass.

Credentials are redacted at index time: AWS keys, generic `api_key=`/`token=` assignments, bearer tokens and raw JWTs, PEM private key blocks, provider tokens (`ghp_`, `sk-`, `npm_`, `xox.`, `AIza`), `scheme://user:pass@host` URLs, and — for shapes no pattern knows — high-entropy values on the right side of any assignment or alone on a line. The value is replaced with `[redacted:<kind>]`; surrounding text stays searchable. `deja sources` shows per-store counts. Opt out with `DEJA_NO_REDACT=1` (unsafe). `deja share` and `deja sync export` re-apply redaction on the way out.

The [security model](docs/SECURITY-MODEL.md) documents data flows, redaction
limits, trust assumptions, and release verification.

## Supported harnesses

<!-- matrix:start -->
| Harness | Store | MCP recall | Auto-recall | Resume | Handoff | Needs |
| --- | --- | :-: | :-: | :-: | :-: | --- |
| Claude Code | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl`<br>`${DEJA_CLAUDE_ROOT}/**/*.jsonl` | ✅ | ✅ | ✅ | ✅ | — |
| Cline | `${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/*/*.messages.json`<br>`<vscode-globalStorage>/saoudrizwan.claude-dev/tasks/*/api_conversation_history.json`<br>`${DEJA_CLINE_ROOT}/*/*.messages.json`<br>`${DEJA_CLINE_ROOTS}/tasks/*/api_conversation_history.json` | ✅ | — | ✅ | paste | — |
| Codex CLI | `${CODEX_HOME:-~/.codex}/sessions/**/rollout-*.jsonl`<br>`${CODEX_HOME:-~/.codex}/history.jsonl`<br>`${DEJA_CODEX_ROOT}/sessions/**/rollout-*.jsonl` | ✅ | ✅ | ✅ | ✅ | — |
| opencode | `~/.local/share/opencode/opencode.db`<br>`${XDG_DATA_HOME}/opencode/opencode.db`<br>`${DEJA_OPENCODE_DB}` | ✅ | ✅ | ✅ | ✅ | sqlite3 |
| aider | `~/.aider.chat.history.md`<br>`${AIDER_CHAT_HISTORY_FILE}`<br>`${DEJA_AIDER_ROOTS}/**/.aider.chat.history.md` | — | — | — | ✅ | — |
| Gemini CLI | `${GEMINI_CLI_HOME:-~}/.gemini/tmp/*/chats/**/*.{json,jsonl}`<br>`${DEJA_GEMINI_ROOT}/tmp/*/chats/**/*.{json,jsonl}` | ✅ | — | — | ✅ | — |
| Cursor | `~/Library/Application Support/Cursor/User/{globalStorage,workspaceStorage/*}/state.vscdb`<br>`~/.config/Cursor/User/{globalStorage,workspaceStorage/*}/state.vscdb`<br>`${CURSOR_CONFIG_DIR:-~/.cursor}/projects/**/agent-transcripts/**/*.jsonl`<br>`${DEJA_CURSOR_ROOT}`<br>`${DEJA_CURSOR_CLI_ROOT}` | ✅ | — | — | ✅ | sqlite3 (IDE chats) |
| Antigravity | `~/.gemini/antigravity*/brain/*/.system_generated/logs/transcript.jsonl`<br>`${DEJA_ANTIGRAVITY_ROOT}/brain/*/.system_generated/logs/transcript.jsonl` | ✅ | — | ✅ | paste | — |
| Grok Build | `${GROK_HOME:-~/.grok}/sessions/**/updates.jsonl`<br>`${DEJA_GROK_ROOT}/sessions/**/updates.jsonl` | ✅ | — | — | ✅ | — |
| Qwen Code | `${DEJA_QWEN_ROOT:-~/.qwen}/projects/*/chats/*.jsonl` | ✅ | — | — | ✅ | — |
| Kimi Code | `${KIMI_CODE_HOME:-~/.kimi-code}/sessions/*/*/agents/main/wire.jsonl`<br>`${DEJA_KIMI_ROOT}/sessions/*/*/agents/main/wire.jsonl` | ✅ | — | ✅ | paste | — |
| pi | `${DEJA_PI_ROOT:-~/.pi/agent/sessions}/**/*.jsonl` | ✅ | — | ✅ | ✅ | — |
| OpenClaw | `${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/*/sessions/*.jsonl`<br>`${DEJA_OPENCLAW_ROOT}/*/sessions/*.jsonl` | ✅ | — | — | paste | — |
| Copilot CLI | `${DEJA_COPILOT_ROOT:-~/.copilot/session-state}/*/events.jsonl` | ✅ | — | ✅ | ✅ | — |
| Roo Code | `<vscode-globalStorage>/rooveterinaryinc.roo-cline/tasks/*/api_conversation_history.json`<br>`${DEJA_ROO_ROOTS}/tasks/*/api_conversation_history.json` | — | — | — | paste | — |
<!-- matrix:end -->

Custom locations via `DEJA_CLAUDE_ROOT`, `DEJA_CODEX_ROOT`, `DEJA_OPENCODE_DB`, `DEJA_AIDER_ROOTS`, `DEJA_GEMINI_ROOT`, `DEJA_CURSOR_ROOT`, `DEJA_CURSOR_CLI_ROOT`, `DEJA_ANTIGRAVITY_ROOT`, `DEJA_GROK_ROOT`, `DEJA_QWEN_ROOT`, `DEJA_INDEX_DIR`. Each agent's own relocation variable is honored too: `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GEMINI_CLI_HOME`, `CURSOR_CONFIG_DIR`, `GROK_HOME`, `AIDER_CHAT_HISTORY_FILE`, and `XDG_DATA_HOME` for opencode on Linux.

`DEJA_RECALL=safe` is the default: SessionStart recall stays in the current project, filters weak or duplicate results, prefers the last 90 days, and injects at most 2KB. `DEJA_RECALL=aggressive` searches across projects and raises the injection cap to 4KB. `DEJA_RECALL=off` disables SessionStart recall output.

### Session format registry

The [session format registry](docs/registry/README.md) documents the observed store paths, record schemas, role mapping, timestamps, and compatibility notes for each supported harness. Synthetic fixtures keep those descriptions checked against the parsers.

## Performance

Measured on a real corpus — 1,250+ sessions, ~3.3GB across three harnesses:

| Measurement | Result |
| --- | --- |
| Warm search | **~12 ms** typical, ~25 ms worst-case |
| Cold index (once) | ~10 s |
| Index size | ~2.4% of corpus |

The index is incremental: when a session file grows, only that file is re-read.

## Benchmarks

Run the reproducible recall benchmark with:

```sh
deja bench recall
deja bench recall --json
```

The synthetic set is currently saturated by lexical search (recall@5 1.00 at ~0.7 ms median), so it serves as a regression floor for ranking changes rather than a bragging number; CI fails if recall drops. The corpus generator and the relevance labels are ordinary reviewed Go — audit what "relevant" means before trusting any figure, ours included. With a local embedding endpoint up, the same command reports the hybrid column.

The context experiment is reproducible with `deja bench context --json`. It builds 30 seeded multi-session task chains and five negative controls, then compares deja-recall (the real index and SessionStart digest), full-history, naive substring grep, and cold context. Tokens are approximated as bytes/4. Coverage is the fraction of generator-defined ground-truth fact strings present verbatim; audit that generator before trusting the figures.

Run with the default seed (`1`):

| Arm | Median tokens | P10-P90 tokens | Median coverage | Negative-control median tokens |
| --- | ---: | ---: | ---: | ---: |
| deja-recall | 278 | 278-278 | 1.00 | 0 |
| full-history | 16,919 | 11,899-22,092 | 1.00 | 14,920 |
| naive-grep | 57,489 | 40,413-74,837 | 1.00 | 0 |
| cold | 0 | 0-0 | 0.00 | 0 |

Prior sessions in the generated corpus carry realistic log-noise bulk; without it the full-history arm looks artificially cheap and the comparison is meaningless. On this corpus the recall digest reaches the same fact coverage as grepping the raw logs for about 200x fewer tokens — and about 60x fewer than replaying the matched sessions in full — while injecting nothing on the negative-control chains where no prior fact is relevant.

## How it works

Local inverted index in `~/.cache/deja`: parse JSONL/SQLite stores → redact credentials → `records.bin` + token buckets → `manifest.gob` tracks per-file state so repeat runs only ingest what changed. The MCP server, stats, share and sync all read the same index. Details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

**Privacy:** indexing and search are local. Network is used only by `deja update`, `deja sync ssh`, and the `deja doctor` version check. Local files in, local cache out.

## FAQ

**Does anything leave my machine?** Indexing and search are local. `deja update` downloads releases from GitHub, and user-invoked `deja sync ssh` transfers redacted batches through the system SSH client. Directory exports and shares go only to the destination you choose. See the [security model](docs/SECURITY-MODEL.md#data-flows) for the full data flow.

**How is this different from cass?**
[cass](https://github.com/Dicklesworthstone/coding_agent_session_search) is the kitchen-sink take on session search: 22 providers, Rust, optional semantic embeddings, a TUI. deja is the opposite bet — one small Go binary, pure lexical, fifteen harnesses, zero setup — plus the memory-layer pieces around it: auto-recall, redaction, share, sync.

[engram](https://github.com/Gentleman-Programming/engram) is the strongest of the record-forward memory tools: the agent calls `mem_save` and curated notes accumulate in SQLite. Curation buys it conflict detection — deja now surfaces conflicts too, between accepted notes at promote time — but it starts empty, only knows what an agent decided to save, and can't answer for the months of sessions that happened before it was installed. deja starts full: the transcripts are the memory, no cooperation required.

**And from MemPalace / Mem0 / Letta?**
Those are memory *platforms*: a Python runtime, embedding models, a vector store, and capture hooks that only remember what happens after you adopt them. deja has no capture step and no stack — one binary over the logs your agents already wrote, so it knows your history from day one, including everything from before you installed it.

**What about secrets already in my logs?** They stay in the original harness files (that's your agent's data), but they don't enter deja's index, digests, shares or sync exports.

**What about Windows?** Builds exist, CI runs the suite on Windows; macOS/Linux are the battle-tested paths. Field reports welcome: [#9](https://github.com/vshulcz/deja-vu/issues/9).

**Can I exclude a project?** Yes: one case-insensitive pattern per line in `~/.config/deja/exclude` (XDG-aware) or comma-separated in `DEJA_EXCLUDE_PROJECTS`; see the Privacy section above.
**How do I wipe everything?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Contributing

`make build test lint` — see [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness starts in the [parser registry](docs/ARCHITECTURE.md#source-parsers). Current priorities and non-goals are in [ROADMAP.md](ROADMAP.md). Good first issues are labeled.

## License

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
