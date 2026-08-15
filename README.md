<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img src="assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><strong>Your agents already solved this. deja finds it.</strong></p>

<p align="center">Memory tools start empty and record forward. deja starts full. It indexes the sessions your coding agents already wrote to disk, including months of history from before you installed it, and serves them back to any agent over MCP.</p>

<p align="center">One Go binary. No LLM, no embeddings, no API key, nothing leaves the machine.</p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Docs</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">How it compares</a></p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://www.npmjs.com/package/@vshulcz/deja-vu"><img src="https://img.shields.io/npm/v/%40vshulcz%2Fdeja-vu?label=npm" alt="npm"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/vshulcz/deja-vu"><img src="https://api.scorecard.dev/projects/github.com/vshulcz/deja-vu/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://mcptoplist.com/server/io.github.vshulcz%2Fdeja-vu"><img src="https://mcptoplist.com/badge/io.github.vshulcz%2Fdeja-vu.svg" alt="MCP Toplist"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><img src="assets/demo.gif" alt="The same question put to the same agent twice: without memory it has no record of it, with deja it answers with the decision from eight months earlier"></p>

<p align="center"><em>Every line is quoted from two real Claude Code sessions: the same question, the same agent, once without memory and once with deja. Nobody searched anything. The agent called deja itself.</em></p>

<p align="center">
<b>84.9% hit@1</b> on LongMemEval-S &middot; <b>69.8%</b> on LoCoMo &middot; <b>~1.5&nbsp;ms</b> median search over 3.5&nbsp;GB<br>
<sub>Both harnesses ship in this repo and run on the public datasets in minutes. <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Check the numbers yourself.</a></sub>
</p>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

or `brew install vshulcz/tap/deja-vu`, `go install github.com/vshulcz/deja-vu/cmd/deja@latest`,
`npx @vshulcz/deja-vu "query"`. Desktop apps that install MCP servers as bundles can
open the `.mcpb` from the [latest release](https://github.com/vshulcz/deja-vu/releases/latest);
it carries the binary, so there is nothing else to install.

## Wire it into your agents

```sh
deja install --all     # MCP recall for every agent found on this machine
deja install --auto    # same, plus session-start auto-recall where supported
```

Install also builds the first index, so the next agent session is instant rather than
paying for the build. Claude Code, Codex, Cursor, Qwen, OpenClaw and Copilot can take the
same plugin bundle from their own marketplaces instead:

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

One bundle installs into six harnesses. The [agent setup guide](https://vshulcz.github.io/deja-vu/guide/agents.html)
covers the rest: what each harness supports, aider's read-only context file, and the
Windows `cmd /c deja mcp` wrapper.

Install also writes user-level guidance for the harnesses it detects: Claude Code, Codex, opencode, Gemini CLI, Antigravity, Qwen, Kimi Code, pi, Copilot, Cursor, Goose, OpenClaw, Hermes and Roo Code each get it in their own guidance file (or under the configured `XDG_CONFIG_HOME`). Re-run rewrites deja's skill or marked block without changing surrounding user content. Use `deja install --all --no-guidance` to opt out; Grok gets `~/.grok/GROK.md`, which it reads only when a project has no `.grok/GROK.md` of its own. Cursor has no user-level instructions file, so it gets a skill at `~/.cursor/skills/` instead, read only when something looks relevant rather than every session.

That's it. Next session, ask your agent:

> have we dealt with jwt refresh rotation before? check your memory

With `--auto`, don't ask. The agent starts each session already knowing what you solved
in that project.

## What you get

Seventeen coding agents write every conversation to local files. deja turns those files
into a memory layer that any of them can read.

| | |
| --- | --- |
| **Retroactive search** | `deja "connection pool exhausted"` over gigabytes, including everything from before you installed deja. Natural-language questions fall back to a relevance tier. Time is a hint, not a filter. |
| **Cross-agent recall** | Solve it in Codex, Claude remembers. The MCP `recall` tool answers *"we fixed this three weeks ago"* instead of re-debugging it. |
| **Recall at the point of action** | Before an agent edits a file or runs a command, deja names that file's prior decision or that command's working invocation, from a `PreToolUse` hook. |
| **It indexes the work, not just the talk** | The files each turn opened, the commands that ran with their exit status, and the exact spans an edit replaced. That is the part every summary throws away. |
| **It knows what held** | `deja promote <id> --state rejected --note "why"` marks a decision you reverted. Every later hit for that session shows it was tried and rejected, with the reason. Nothing is deleted, and `--state accepted` takes the mark back. |
| **It survives compaction** | Measured over 43 compactions: 77% of decisions survive the summary, 0.2% of the commands. deja hands the session its own specifics back. |
| **It says when the ground moved** | A hit reports *4 files this session touched have changed since*, and says nothing when it cannot tell. It never claims anything is unchanged. |
| **Sync and handoff** | `deja sync ssh laptop` moves memory between machines, append-only, no cloud in the middle. `deja handoff --to codex` packages the live context so you can continue in another agent. |
| **Redaction** | Keys, tokens, JWTs and private key blocks are stripped at index time, so the cache is safe to keep. |

The full feature reference lives in the [docs](https://vshulcz.github.io/deja-vu/).

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
| `deja <query>` | Search every history. Multi-word is AND, quoted phrases require contiguous text, and a query with no exact match then tries word forms and close spellings, which is where a substring reaches its word (`code` finds `opencode`). `--harness`, `--project`, `--since 30d`, `--limit`, `--json`. |
| `deja` | With an index and a terminal: today's sessions, recalls served, a question you asked in more than one session, and a wall your agents keep hitting. |
| `deja ctx <query>` | Markdown digest of the best match, ready to pipe into a prompt. |
| `deja blame <path>` | Which sessions discussed a file, what was decided, and why. |
| `deja files <topic>` | The other direction: which files the work on a subject actually touched. |
| `deja how "<tool>"` | How this machine actually runs a thing, with the real flags, from what agents ran before. |
| `deja fix "<error>"` | What this machine ran after that same error before, when the error did not come back. |
| `deja friction` | Errors that hit three or more separate sessions, with the harnesses named. |
| `deja restore <path>` | Hand back a span an agent replaced, from the `old_string` its edit recorded. Never writes over the original. |
| `deja promote <id>` | Distill a session into a curated note with provenance, tags and a lifecycle state. Notes outrank raw transcripts. |
| `deja resume <id>` | Reopen a found session in its native harness. |
| `deja share <id>` | A sanitized session digest for a colleague, with secrets already scrubbed. |
| `deja sync export/import/ssh` | Move memory between machines. Watermarked, append-only, idempotent. |
| `deja view` | Your whole memory as one local HTML file. No server, nothing leaves the machine. |
| `deja doctor [--deep]` | Self-diagnosis, and with `--deep`, proof of the index against the sources. |
| `deja stats` | Your agent work, wrapped. `--card` writes an SVG for a profile, `--html` a browsable timeline. |
| `deja mcp` | The stdio MCP server, which is what `deja install` wires in. |

Full reference: [commands](https://vshulcz.github.io/deja-vu/guide/commands.html) and
[JSON output](docs/json-output.md).

### MCP tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `recall` | `query`, `harness?`, `limit?` | Dense matching snippets, capped at 4KB. |
| `recall_context` | `query`, `harness?` | Markdown digest of the best-matching session. |
| `blame` | `path`, `harness?`, `project?`, `since?`, `limit?` | Sessions that discussed a file. |
| `remember` | `text`, `project?` | Stores a durable decision for later recall. |

## Supported harnesses

<!-- matrix:start -->
| Harness | Store | MCP recall | Auto-recall | Skill | Command | Resume | Handoff | Needs |
| --- | --- | :-: | :-: | :-: | :-: | :-: | :-: | --- |
| Claude Code | `${CLAUDE_CONFIG_DIR:-~/.claude}/projects/**/*.jsonl`<br>`${DEJA_CLAUDE_ROOT}/**/*.jsonl` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Cline | `${CLINE_SESSION_DATA_DIR:-${CLINE_DATA_DIR:-${CLINE_DIR:-~/.cline}/data}/sessions}/*/*.messages.json`<br>`<vscode-globalStorage>/saoudrizwan.claude-dev/tasks/*/api_conversation_history.json`<br>`${DEJA_CLINE_ROOT}/*/*.messages.json`<br>`${DEJA_CLINE_ROOTS}/tasks/*/api_conversation_history.json` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Codex CLI | `${CODEX_HOME:-~/.codex}/sessions/**/rollout-*.jsonl`<br>`${CODEX_HOME:-~/.codex}/history.jsonl`<br>`${DEJA_CODEX_ROOT}/sessions/**/rollout-*.jsonl` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| opencode | `~/.local/share/opencode/opencode.db`<br>`${XDG_DATA_HOME}/opencode/opencode.db`<br>`${DEJA_OPENCODE_DB}` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3 |
| aider | `~/.aider.chat.history.md`<br>`${AIDER_CHAT_HISTORY_FILE}`<br>`${DEJA_AIDER_ROOTS}/**/.aider.chat.history.md` | ⚠ | ✅ | ? | ⚠ | ✕ | ✅ | deja aider |
| Gemini CLI | `${GEMINI_CLI_HOME:-~}/.gemini/tmp/*/chats/**/*.{json,jsonl}`<br>`${DEJA_GEMINI_ROOT}/tmp/*/chats/**/*.{json,jsonl}` | ✅ | ✅ | ✅ | ✅ | — | ✅ | — |
| Cursor | `~/Library/Application Support/Cursor/User/{globalStorage,workspaceStorage/*}/state.vscdb`<br>`~/.config/Cursor/User/{globalStorage,workspaceStorage/*}/state.vscdb`<br>`${CURSOR_CONFIG_DIR:-~/.cursor}/projects/**/agent-transcripts/**/*.jsonl`<br>`${DEJA_CURSOR_ROOT}`<br>`${DEJA_CURSOR_CLI_ROOT}` | ✅ | ✅ | ✅ | ✅ | — | ✅ | sqlite3 (IDE chats) |
| Antigravity | `~/.gemini/antigravity*/brain/*/.system_generated/logs/transcript.jsonl`<br>`${DEJA_ANTIGRAVITY_ROOT}/brain/*/.system_generated/logs/transcript.jsonl` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Grok Build | `${GROK_HOME:-~/.grok}/sessions/**/updates.jsonl`<br>`${DEJA_GROK_ROOT}/sessions/**/updates.jsonl`<br>`${GROK_HOME:-~/.grok}/grok.db` | ✅ | ✅ | ✅ | ✅ | ? | ✅ | sqlite3 (grok-dev store) |
| Hermes | `~/.hermes/profiles/*/state.db`<br>`${DEJA_HERMES_PROFILES_ROOT}/*/state.db`<br>`${DEJA_HERMES_DB}` | ✅ | ✅ | ✅ | ✅ | ✅ | paste | sqlite3 |
| Goose | `${GOOSE_PATH_ROOT}/data/sessions/sessions.db`<br>`~/.local/share/goose/sessions/*.jsonl`<br>`~/.local/share/goose/sessions/sessions.db`<br>`${XDG_DATA_HOME}/goose/sessions/*.jsonl`<br>`${XDG_DATA_HOME}/goose/sessions/sessions.db`<br>`${DEJA_GOOSE_ROOT}/sessions/*.jsonl`<br>`${DEJA_GOOSE_ROOT}/sessions/sessions.db`<br>`${DEJA_GOOSE_DB}` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | deja goose |
| Qwen Code | `${DEJA_QWEN_ROOT:-~/.qwen}/projects/*/chats/*.jsonl` | ✅ | ✅ | ✅ | ✅ | — | ✅ | — |
| Kimi Code | `${KIMI_CODE_HOME:-~/.kimi-code}/sessions/*/*/agents/main/wire.jsonl`<br>`${DEJA_KIMI_ROOT}/sessions/*/*/agents/main/wire.jsonl` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| pi | `${DEJA_PI_ROOT:-~/.pi/agent/sessions}/**/*.jsonl` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| OpenClaw | `${OPENCLAW_STATE_DIR:-~/.openclaw}/agents/*/sessions/*.jsonl`<br>`${DEJA_OPENCLAW_ROOT}/*/sessions/*.jsonl` | ✅ | ✅ | ✅ | ✅ | — | paste | — |
| Copilot CLI | `${DEJA_COPILOT_ROOT:-~/.copilot/session-state}/*/events.jsonl` | ✅ | ✕ | ✅ | ✅ | ✅ | ✅ | — |
| Roo Code | `<vscode-globalStorage>/rooveterinaryinc.roo-cline/tasks/*/api_conversation_history.json`<br>`${DEJA_ROO_ROOTS}/tasks/*/api_conversation_history.json`<br>`~/.vscode-mock/global-storage/tasks/*/api_conversation_history.json` | ✅ | ⚠ | ✅ | ✅ | ✕ | paste | — |

✅ works &middot; — possible, not built yet &middot; ✕ the harness has no such mechanism &middot; ⚠ blocked by an upstream bug &middot; ? not investigated


- aider `mcp` — aider has no MCP client. Not a design limit but an unimplemented feature: several open requests and an open PR adding it, so this becomes work the day they ship it. Until then recall reaches aider through the read: context file (https://github.com/Aider-AI/aider/pull/5539)
- aider `command` — the slash command set is built in; adding custom commands is an open feature request upstream, not something a third party can wire today (https://github.com/Aider-AI/aider/issues/894)
- Copilot CLI `auto` — Copilot runs the hooks but drops their context, so recall there is MCP plus the skill
- Roo Code `auto` — Roo's hooks are in flight upstream, not shipped: PR #10785 implements a Claude Code-style hooks system, #11128 is Hooks beta and #11663 Hooks phase 1, all still open, and no release names lifecycle hooks. A user's bug report about PreToolUse coverage (#10834) shows they work on a branch build. Becomes work the day one of those merges (https://github.com/RooCodeInc/Roo-Code/pull/10785)
<!-- matrix:end -->

Custom store locations go through `DEJA_*_ROOT` variables, and each agent's own relocation
variable is honored too. The [session format registry](docs/registry/README.md) documents
the observed paths, record schemas and role mapping per harness, with synthetic fixtures
keeping those descriptions checked against the parsers.

## Semantic recall (optional)

Point `deja embed` at a local Ollama, LM Studio or OpenAI-compatible endpoint with
`DEJA_EMBED_URL` and rephrased queries still hit. Without a reachable runtime, lexical
search and MCP recall continue unchanged.

The vector sidecar sits beside the index as `.vectors.bin`, not inside `index.db`.
Float32 vectors cost roughly 4 MB per 1k messages for a 1,024 dimension model. Embedding
is local, and it never sends raw source files, only the redacted indexed text truncated to
about 2k characters.

## Performance

Measured on a real corpus of 1,250+ sessions, roughly 3.3GB across three harnesses:

| Measurement | Result |
| --- | --- |
| Warm search | **~1.5 ms** median, ~14 ms on the most common word in the store |
| Cold index (once) | ~10 s |
| Index size | ~2.3% of corpus |

The index is incremental. When a session file grows, only that file is re-read.

## Benchmarks

```sh
deja bench recall     # ranking regression floor, CI fails if recall drops
deja bench context    # 30 seeded task chains plus five negative controls
```

The context experiment compares deja-recall against full-history, naive grep and cold
context. With the default seed:

| Arm | Median tokens | Median coverage | Negative-control tokens |
| --- | ---: | ---: | ---: |
| deja-recall | 286 | 1.00 | 0 |
| full-history | 16,919 | 1.00 | 14,920 |
| naive-grep | 57,489 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

Same fact coverage as grepping the raw logs for about 200x fewer tokens, and about 60x
fewer than replaying the matched sessions in full, while injecting nothing on the chains
where no prior fact is relevant. The corpus generator and the relevance labels are
ordinary reviewed Go. Audit what "relevant" means before trusting any figure, ours
included.

## Privacy

Indexing and search are local. The network is used only by `deja update`, `deja sync ssh`,
and the version check in `deja doctor`.

Credentials are redacted at index time: AWS keys, `api_key=` and `token=` assignments,
bearer tokens and raw JWTs, PEM private key blocks, provider tokens, `scheme://user:pass@host`
URLs, and high-entropy values for shapes no pattern knows. The value becomes
`[redacted:<kind>]` and the surrounding text stays searchable. `deja share` and
`deja sync export` re-apply redaction on the way out.

`deja forget` removes sessions from a rebuilt index and writes tombstones, so a later
`deja index` cannot restore them from the source history. `--unforget` lifts a tombstone.
Project exclusions are one pattern per line in `~/.config/deja/exclude`.

The [security model](docs/SECURITY-MODEL.md) documents data flows, redaction limits, trust
assumptions and release verification.

## How it works

Local inverted index in `~/.cache/deja`: parse the JSONL and SQLite stores, redact
credentials, write `records.bin` plus token buckets, and track per-file state in
`manifest.gob` so repeat runs only ingest what changed. The MCP server, stats, share and
sync all read that one index. Details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## FAQ

**Does anything leave my machine?** No, unless you ask it to. See the
[data flows](docs/SECURITY-MODEL.md#data-flows).

**How is this different from cass?**
[cass](https://github.com/Dicklesworthstone/coding_agent_session_search) is the
kitchen-sink take on session search: 22 providers, Rust, optional embeddings, a TUI. deja
is the opposite bet, one small Go binary with pure lexical search over seventeen harnesses
and zero setup, plus the memory-layer pieces around it.

**And from engram, MemPalace, Mem0, Letta?**
Those record forward. [engram](https://github.com/Gentleman-Programming/engram) is the
strongest of them, but it starts empty and only knows what an agent chose to save. The
platforms add a Python runtime, embedding models and a vector store on top of the same
limitation. deja has no capture step and no stack: the transcripts are the memory, so it
knows your history from day one, including everything from before you installed it.

**What about secrets already in my logs?** They stay in the original harness files, which
are your agent's data. They do not enter deja's index, digests, shares or sync exports.

**What about Windows?** Builds exist and CI runs the suite there. macOS and Linux are the
battle-tested paths. Field reports welcome in [#9](https://github.com/vshulcz/deja-vu/issues/9).

**How do I wipe everything?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Contributing

`make build test lint`, then [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness starts in
the [parser registry](docs/ARCHITECTURE.md#source-parsers). Priorities and non-goals are in
[ROADMAP.md](ROADMAP.md). Good first issues are labeled.

## License

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
