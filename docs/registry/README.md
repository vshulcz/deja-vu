# Session format registry

This registry records observed on-disk session formats for the harnesses that deja parses. Each entry describes store discovery, file layout, message records, role and timestamp handling, and known compatibility behavior. These are observations of upstream output, not specifications published by the harness vendors.

[`registry.json`](registry.json) is the machine-readable index. The files under [`fixtures/registry`](../../fixtures/registry) are synthetic conformance samples shaped like upstream records. `internal/sources/registry_test.go` checks the index against deja's loader list and runs each fixture through its parser.

## Entries

| Harness | Format |
| --- | --- |
| [Claude Code](claude-code.md) | JSONL transcript |
| [Codex CLI](codex.md) | rollout and history JSONL |
| [opencode](opencode.md) | SQLite relational store |
| [Cursor](cursor.md) | SQLite key-value store and JSONL transcript |
| [Gemini CLI](gemini.md) | session JSON and replayable JSONL |
| [aider](aider.md) | append-only Markdown |
| [Antigravity](antigravity.md) | JSONL transcript |
| [Grok Build](grok.md) | ACP update JSONL with JSON metadata |
| [Qwen Code](qwen.md) | JSONL transcript |

## Reporting drift

Open an issue with the harness name and version, operating system, observed store path, and the smallest redacted record that shows the difference. State whether the change affects discovery, session metadata, roles, content, or timestamps. Do not attach a real session database or unredacted transcript.

## Adding or updating a format

1. Update the harness reference page from observed records and note the drift under **Known quirks and drift**.
2. Add a synthetic fixture that contains no user data or credentials.
3. Add or update the `registry.json` entry. Paths are repository-relative; store paths use environment-variable placeholders where applicable.
4. Update the parser and its focused tests, then run the registry conformance test and the full suite.
5. Set **Last verified** to the observation date and **deja parser version** to the version used for verification.

SQLite fixture sources are stored as SQL so changes remain reviewable. The conformance test materializes them in a temporary directory before parsing.
