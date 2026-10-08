# Session format registry

This registry records observed on-disk session formats for the harnesses that deja parses. Each entry describes store discovery, file layout, message records, role and timestamp handling, and known compatibility behavior. These are observations of upstream output, not specifications published by the harness vendors.

[`registry.json`](registry.json) is the machine-readable index. The files under [`fixtures/registry`](../../fixtures/registry) are synthetic conformance samples shaped like upstream records. `internal/sources/registry_test.go` checks the index against deja's loader list and runs each fixture through its parser.

## Entries

| Harness | Format |
| --- | --- |
| [Claude Code](claude-code.md) | JSONL transcript |
| [Codex CLI](codex.md) | rollout and history JSONL |
| [opencode](opencode.md) | SQLite relational store, plus a JSON diff file per session |
| [Cursor](cursor.md) | SQLite key-value store and JSONL transcript |
| [Gemini CLI](gemini.md) | session JSON and replayable JSONL |
| [aider](aider.md) | append-only Markdown |
| [Amp](amp.md) | one JSON object per thread |
| [Antigravity](antigravity.md) | JSONL transcript |
| [Grok Build](grok.md) | ACP update JSONL with JSON metadata |
| [Goose](goose.md) | legacy JSONL and SQLite session store |
| [Qwen Code](qwen.md) | JSONL transcript |
| [pi](pi.md) | JSONL transcript |
| [omp (Oh My Pi)](omp.md) | JSONL transcript |
| [prime-agent (PrimeIntellect)](prime.md) | JSONL transcript |
| [Cline](cline.md) | task JSON, two store generations |
| [Kimi Code](kimi.md) | per-agent wire JSONL |
| [OpenClaw](openclaw.md) | append-only pi-format JSONL |
| [Copilot CLI](copilot.md) | session event JSONL |
| [VS Code Copilot Chat](copilot-chat.md) | JSON/JSONL mutation log in VS Code workspaceStorage, and the extension's own event-log transcripts |
| [Roo Code](roo.md) | task JSON in VS Code globalStorage |
| [Kilo Code](kilocode.md) | task JSON in VS Code globalStorage, plus the CLI's OpenCode-schema SQLite |
| [Kiro](kiro.md) | one JSONL per session from the CLI, another from the IDE |
| [Senpi](senpi.md) | pi's session JSONL under its own agent directory |
| [gajae-code](gjc.md) | pi's session JSONL, sub-agent passes one level down |
| [Kimchi Coding](kimchi.md) | pi's session JSONL, flat root |
| [Command Code](commandcode.md) | session header + message envelopes, JSONL per session |
| [ZCode](zcode.md) | flat role/content JSONL per session, plus the CLI's OpenCode-schema SQLite |
| [CodeWhale](codewhale.md) | one JSON document per session, Anthropic-shaped content blocks |
| [Junie](junie.md) | one event-log JSONL per session directory, blocks folded by step |
| [JetBrains AI Assistant](jetbrains.md) | chats in each IDE's workspace XML, agent chats in base64 JSON task logs |
| [CodeBuddy Code](codebuddy.md) | Claude Code's project tree, OpenAI Responses-style items per line |
| [Reasonix](reasonix.md) | flat role/content JSONL per session, clock and workspace in a sidecar; 1.x keeps a zstd-framed event log per session directory |
| [TRAE CLI](trae.md) | Codex rollouts under `~/.trae/cli`, user turns from events only, tool calls in `history_mutation` |
| [Muse Code](muse.md) | event-sourced JSONL per session directory, sharded by day |
| [Cherry Studio](cherrystudio.md) | Claude Code transcripts under the desktop app's data, one snapshot per stream chunk |
| [Continue](continue.md) | one JSON document per session, list beside it |
| [Crush](crush.md) | one SQLite store per project, registry in the data home |
| [Hermes](hermes.md) | SQLite state store, and Postgres when configured |
| [DeepSeek Harness](deepseek.md) | append-only session log, zstd-framed JSONL |
| [Zed](zed.md) | SQLite thread store, zstd-compressed bodies |

## Reporting drift

Open an issue with the harness name and version, operating system, observed store path, and the smallest redacted record that shows the difference. State whether the change affects discovery, session metadata, roles, content, or timestamps. Do not attach a real session database or unredacted transcript.

## Adding or updating a format

1. Update the harness reference page from observed records and note the drift under **Known quirks and drift**.
2. Add a synthetic fixture that contains no user data or credentials.
3. Add or update the `registry.json` entry. Paths are repository-relative; store paths use environment-variable placeholders where applicable.
4. Update the parser and its focused tests, then run the registry conformance test and the full suite.
5. Set **Last verified** to the observation date, here and in `registry.json` — those two are compared, so a page cannot claim a check the registry does not have.

SQLite fixture sources are stored as SQL so changes remain reviewable. The conformance test materializes them in a temporary directory before parsing.
