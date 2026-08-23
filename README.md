<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img src="assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Memory for coding agents, starting with the history you already have.</b></p>

<p align="center">Your agent is about to re-debug something you fixed in March. deja indexes the
sessions Claude Code, Codex, Cursor and every other agent on this machine already wrote to
disk, and hands the right one back when it is needed.</p>

<p align="center"><img src="assets/demo.gif" width="720" alt="The same question put to the same agent twice: without memory it has no record of it, with deja it answers with the decision from eight months earlier"></p>

<p align="center"><sub><em>Nobody searched anything — the agent called deja itself. Every line is quoted from two real sessions.</em></sub></p>

<p align="center"><b>Every memory tool starts empty and records forward. deja starts full.</b></p>

<p align="center">
<b>85.3% hit@1</b> on LongMemEval-S &middot; <b>69.6%</b> on LoCoMo &middot; <b>sub-millisecond</b> lookups over 5&nbsp;GB of history<br>
<sub>Both harnesses ship in this repo and run on the public datasets in minutes &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">check the numbers yourself</a></sub>
</p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://mcptoplist.com/server/io.github.vshulcz%2Fdeja-vu"><img src="https://mcptoplist.com/badge/io.github.vshulcz%2Fdeja-vu.svg" alt="MCP Toplist"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center">English | <a href="README.zh.md">中文</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Docs</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">How it compares</a></p>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

<p align="center"><img src="assets/banner.png" width="700" alt="What deja prints after the first index: the mark, the agents it found, and a query taken from your own history"></p>

Ten seconds to install, about ten to index, and it is useful. The second command wires MCP
recall into every agent it finds, turns on session-start recall where the agent supports
it, and builds the first index so the next session does not pay for it.

Start a new agent session and ask it something you worked on months ago:

> have we dealt with jwt refresh rotation before? check your memory

It does not have to be asked, either — with auto-recall the agent already knows what you
solved in that project when the session opens.

<details>
<summary>Other ways to install, and what to do if you want less than all of it</summary>

`brew install vshulcz/tap/deja-vu`, `go install github.com/vshulcz/deja-vu/cmd/deja@latest`,
or `npx @vshulcz/deja-vu "query"` to try it without installing anything. Desktop apps that
take MCP servers as bundles can open the `.mcpb` from the
[latest release](https://github.com/vshulcz/deja-vu/releases/latest); it carries the binary.

Claude Code, Codex, Cursor, Qwen, OpenClaw and Copilot can take the same plugin bundle from
their own marketplaces instead:

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

On Windows the install script exits with `unsupported OS` — it is a shell script. Take
`deja-vu_<version>_windows_amd64.zip` from the
[latest release](https://github.com/vshulcz/deja-vu/releases/latest) and put `deja.exe` on
your `PATH`, e.g. in `%USERPROFILE%\.local\bin`.

The binary alone is a complete install for searching: index, search, `show`, `ctx`, `blame`,
`--json` and redaction need nothing else. `deja install` is what wires MCP into your agents
and turns on session-start recall — worth having, and optional. On a binary-only setup
`deja doctor` reports every MCP target as `not-wired`, which is that setup working as
intended. `deja warmup` also leaves a skill at `~/.agents/skills/deja-search/SKILL.md`
that teaches an agent the CLI contract — `deja search --json`, `ctx`, `blame`, how to read
`tier` and `total` — so it knows history is searchable without MCP. The copy in the repo is
[`skills/deja-search/SKILL.md`](skills/deja-search/SKILL.md).

`deja install --all` is `--auto` without the session-start recall: agents answer from memory
when they decide to call it, rather than starting each session with it. The
[agent setup guide](https://vshulcz.github.io/deja-vu/guide/agents.html) covers what each
harness supports, aider's read-only context file, and the Windows `cmd /c deja mcp` wrapper.

</details>

<details>
<summary>What gets written into each agent's own guidance file</summary>

Install also writes user-level guidance for the harnesses it detects: Claude Code, Codex, opencode, Gemini CLI, Antigravity, Qwen, Kimi Code, pi, Copilot, Cursor, Goose, OpenClaw, Hermes, Roo Code, omp, DeepSeek Harness and Zed each get it in their own guidance file (or under the configured `XDG_CONFIG_HOME`). Re-run rewrites deja's skill or marked block without changing surrounding user content. Use `deja install --all --no-guidance` to opt out; Grok gets `~/.grok/GROK.md`, which it reads only when a project has no `.grok/GROK.md` of its own. Cursor has no user-level instructions file, so it gets a skill at `~/.cursor/skills/` instead, read only when something looks relevant rather than every session.

</details>

## What you get

**Solve it in Codex. Claude remembers.** Twenty coding agents write every conversation
to local files, and deja turns those files into one memory layer all of them read.

| | |
| --- | --- |
| **Retroactive search** | `deja "connection pool exhausted"` over gigabytes, including everything from before you installed deja. Natural-language questions fall back to a relevance tier. Time is a hint, not a filter. |
| **Cross-agent recall** | The MCP `recall` tool answers *"we fixed this three weeks ago"* in whichever agent asks, whoever solved it originally. |
| **It survives compaction** | Measured over 43 compactions: the summary keeps 77% of the decisions and 0.2% of the commands you ran. deja hands back the other 99.8%. |
| **Recall at the point of action** | Before an agent edits a file or runs a command, deja names that file's prior decision or that command's working invocation, from a `PreToolUse` hook. When a command fails, a `PostToolUse` hook answers with what followed that same error here before — the pair an agent never thinks to ask for. |
| **It indexes the work, not just the talk** | The files each turn opened, the commands that ran with their exit status, and the exact spans an edit replaced. That is the part every summary throws away. |

<details>
<summary>Four more: rejected decisions, staleness, sync and handoff, redaction</summary>

| | |
| --- | --- |
| **It knows what held** | `deja promote <id> --state rejected --note "why"` marks a decision you reverted. Every later hit for that session shows it was tried and rejected, with the reason. Nothing is deleted, and `--state accepted` takes the mark back. |
| **It says when the ground moved** | A hit reports *4 files this session touched have changed since*, and says nothing when it cannot tell. It never claims anything is unchanged. |
| **Sync and handoff** | `deja sync ssh laptop` moves memory between machines, append-only, no cloud in the middle. `deja handoff --to codex` packages the live context so you can continue in another agent. |
| **Redaction** | Keys, tokens, JWTs and private key blocks are stripped at index time, so the cache is safe to keep. |

</details>

### Your own work, wrapped

`deja stats --card` draws it in the terminal; give it a filename and it writes an
SVG for a profile README.

<p align="center"><img src="docs/assets/stats-card-demo.svg" width="760" alt="deja stats card: a year of agent sessions as a heatmap, the agents they came from, and the longest one"></p>

The full feature reference lives in the [docs](https://vshulcz.github.io/deja-vu/).

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

## CLI

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

**Ask your history**

| Command | What it does |
| --- | --- |
| `deja <query>` | Search every history. Multi-word is AND and quoted phrases require contiguous text; a query with no exact match then tries word forms and close spellings, which is where a substring reaches its word (`code` finds `opencode`). |
| `deja` | With an index and a terminal: today's sessions, recalls served, a question you asked in more than one session, and a wall your agents keep hitting. |
| `deja blame <path>` | Which sessions discussed a file, what was decided, and why. |
| `deja files <topic>` | The other direction: which files the work on a subject actually touched. |
| `deja how <tool>` | How this machine actually runs a thing, with the real flags, from what agents ran before. |
| `deja fix <error>` | What this machine ran after that same error before, when the error did not come back. |
| `deja friction` | Errors that hit three or more separate sessions, with the harnesses named. |

<details>
<summary>Using what it finds, and moving it between machines</summary>

**Use what it finds**

| Command | What it does |
| --- | --- |
| `deja ctx <query>` | Markdown digest of the best match, ready to pipe into a prompt. |
| `deja resume <id>` | Reopen a found session in its native harness. |
| `deja restore <path>` | Hand back a span an agent replaced, from the `old_string` its edit recorded. Never writes over the original. |
| `deja promote <id>` | Distill a session into a curated note with provenance, tags and a lifecycle state. Notes outrank raw transcripts. |
| `deja share <id>` | A sanitized session digest for a colleague, with secrets already scrubbed. |

**Move it and check it**

| Command | What it does |
| --- | --- |
| `deja sync export/import/ssh` | Move memory between machines. Watermarked, append-only, idempotent. |
| `deja view` | Your whole memory as one local HTML file. No server, nothing leaves the machine. |
| `deja stats` | Your agent work, wrapped. `--card` draws it in the terminal, `--card <file>.svg` writes one for a profile, `--html` a browsable timeline. |
| `deja doctor [--deep]` | Self-diagnosis, and with `--deep`, proof of the index against the sources. |
| `deja mcp` | The stdio MCP server, which is what `deja install` wires in. |

</details>

Full reference: [commands](https://vshulcz.github.io/deja-vu/guide/commands.html) and
[JSON output](docs/json-output.md).

### MCP tools

The server exposes `recall`, `recall_context`, `blame`, `fix`, `how` and `remember`.
`deja install` wires them in, so this is only needed to configure an agent by hand.

<details>
<summary>Arguments and return shapes</summary>

| Tool | Arguments | Returns |
| --- | --- | --- |
| `recall` | `query`, `harness?`, `limit?`, `offset?` | Dense matching snippets, capped at 4KB. |
| `recall_context` | `query`, `harness?` | Markdown digest of the best-matching session. |
| `blame` | `path`, `harness?`, `project?`, `since?`, `limit?`, `all?` | Sessions that discussed a file. |
| `fix` | `error`, `project?`, `limit?` | What this machine ran after that same error before. |
| `how` | `what`, `project?`, `limit?` | The real invocation, from what agents ran here. |
| `remember` | `text`, `project?`, `tags?` | Stores a durable decision for later recall. |

</details>

## Supported harnesses

<!-- matrix:start -->
Claude Code &middot; Cline &middot; Codex CLI &middot; opencode &middot; aider &middot; Gemini CLI &middot; Cursor &middot; Antigravity &middot; Grok Build &middot; Hermes &middot; Goose &middot; Qwen Code &middot; Kimi Code &middot; pi &middot; omp (Oh My Pi) &middot; OpenClaw &middot; Copilot CLI &middot; Roo Code &middot; DeepSeek Harness &middot; Zed.

<details>
<summary>What each one supports</summary>

| Harness | MCP recall | Auto-recall | Skill | Command | Resume | Handoff | Needs |
| --- | :-: | :-: | :-: | :-: | :-: | :-: | --- |
| Claude Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Cline | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Codex CLI | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| opencode | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3 |
| aider | ⚠ | ✅ | ✕ | ⚠ | ✕ | ✅ | deja aider |
| Gemini CLI | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Cursor | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3 (IDE chats) |
| Antigravity | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Grok Build | ✅ | ✅ | ✅ | ✅ | ? | ✅ | sqlite3 (grok-dev store) |
| Hermes | ✅ | ✅ | ✅ | ✅ | ✅ | paste | sqlite3 |
| Goose | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | deja goose |
| Qwen Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Kimi Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| pi | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| omp (Oh My Pi) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| OpenClaw | ✅ | ✅ | ✅ | ✅ | ✅ | paste | — |
| Copilot CLI | ✅ | ✕ | ✅ | ✅ | ✅ | ✅ | — |
| Roo Code | ✅ | ⚠ | ✅ | ✅ | ✕ | paste | — |
| DeepSeek Harness | ✅ | ✅ | ✅ | ✅ | ✕ | paste | zstd |
| Zed | ✅ | ✕ | ✅ | ✅ | ✕ | paste | sqlite3 + zstd |

✅ works &middot; — possible, not built yet &middot; ✕ the harness has no such mechanism &middot; ⚠ blocked by an upstream bug &middot; ? not investigated

</details>
<!-- matrix:end -->

Custom store locations go through `DEJA_*_ROOT` variables, and each agent's own relocation
variable is honored too. The
[session format registry](https://vshulcz.github.io/deja-vu/registry/README.html) documents
the observed paths, record schemas and role mapping per harness, with synthetic fixtures
keeping those descriptions checked against the parsers.

### Harnesses with a package of their own

`deja install --auto` wires all five of these like every other harness, and
that stays the shortest path. They also have a package in their own ecosystem,
for people who install extensions there rather than from a CLI:

| Harness | Package | Install |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | plugin `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu` then `codex plugin add deja-vu@deja-vu` |
| Grok Build | plugin `deja` | `grok plugin marketplace add xai-org/plugin-marketplace` then `grok plugin install deja` |

Either path is enough on its own, and having both is not a problem: the
opencode, dsh, Kimi, Grok and Codex packages read what `deja install` wrote and
contribute only what is missing, and in Zed both halves use one server id, so
there is nothing to have twice whichever order you install in.

Each uses the deja you already have; the copy it bundles is only the fallback.

## Semantic recall (optional)

Point `deja embed` at a local Ollama, LM Studio or OpenAI-compatible endpoint with
`DEJA_EMBED_URL` and rephrased queries still hit. Without a reachable runtime, lexical
search and MCP recall continue unchanged.

<details>
<summary>Where the vectors live and what they cost</summary>

The sidecar sits beside the index as `.vectors.bin`, not inside `index.db`. Float32 vectors
cost roughly 4 MB per 1k messages for a 1,024 dimension model. Embedding is local, and it
never sends raw source files, only the redacted indexed text truncated to about 2k
characters.

</details>

## Proof

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

Measured on a real store of 1,551 sessions and 143k messages — 5.2 GB across nine
harnesses:

| Measurement | Result |
| --- | --- |
| Lookup, in process | **~0.4 ms** median (`deja bench recall`), ~25 ms on the LongMemEval-S haystacks |
| `deja <query>`, end to end | ~0.2 s median on that store: process start, the freshness check over every store, ranking, printing |
| Freshness check alone | ~30 ms when nothing changed |
| Index size | 160 MB, ~3% of corpus |

The index is incremental. When a session file grows, only that file is re-read.

## How it works

Local inverted index in `~/.cache/deja`: parse the JSONL and SQLite stores, redact
credentials, write `records.bin` plus token buckets, and track per-file state in
`manifest.gob` so repeat runs only ingest what changed. The MCP server, stats, share and
sync all read that one index. Details in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## FAQ

**Does anything leave my machine?** No, unless you ask it to. See the
[data flows](docs/SECURITY-MODEL.md#data-flows).

**What about secrets already in my logs?** They stay in the original harness files, which
are your agent's data. They do not enter deja's index, digests, shares or sync exports.

**Will it slow my agent down?** A recall is a lexical lookup against a local index:
~0.4 ms median, and nothing waits on a model. A hook adds the process start and a
freshness check over your stores on top of that — tens of milliseconds on a store of
a few gigabytes.

**Do I have to change how I work?** No. The agent calls recall itself, and with
auto-recall it already knows the project's prior decisions when the session opens.

**How is this different from the other memory tools?**

| | deja | Memory platforms<br>(Mem0, Letta, memU) | Session search<br>(cass) |
| --- | :-: | :-: | :-: |
| Knows work from before you installed it | yes | no | yes |
| Capture step | none, the transcripts are the memory | the agent or your code writes facts | none |
| Needs an LLM or embedding key | no | yes | optional |
| Recalls without being asked | at session start and before a tool runs | no | no |

[engram](https://github.com/Gentleman-Programming/engram) is the strongest of the
record-forward tools and worth your time if that model fits you; it still starts empty and
knows only what an agent chose to save. The
[full comparison](https://vshulcz.github.io/deja-vu/guide/compare.html) covers eleven of them.

**What about Windows?** Builds exist and CI runs the suite there. macOS and Linux are the
battle-tested paths. Field reports welcome in [#9](https://github.com/vshulcz/deja-vu/issues/9).

**How do I wipe everything?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Try it on your own history

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Ten seconds to install, about ten to index. The next session your agent opens, it
already knows what you solved in that project — including everything from before
you installed this.

## Contributing

`make build test lint`, then [CONTRIBUTING.md](CONTRIBUTING.md). Adding a harness starts in
the [parser registry](docs/ARCHITECTURE.md#source-parsers). Priorities and non-goals are in
[ROADMAP.md](ROADMAP.md). Good first issues are labeled.

## License

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
