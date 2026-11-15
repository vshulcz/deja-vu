# CodeArts Desktop

- **ID**: `codearts`
- **Store**: `~/.codearts/kernel/sessions/<launch-bucket>/<session-uuid>/` — `meta.json` + `history.jsonl`
- **Read overrides**: `DEJA_CODEARTS_ROOT` replaces the sessions root; absolute `CODEARTS_HOME` places it under `<CODEARTS_HOME>/kernel/sessions`
- **Format**: JSONL, one message per line, with Rust serde content variants
- **Needs**: nothing extra

CodeArts Desktop (Huawei Cloud CodeArts) writes one session per **inner** directory of a two-level tree: the outer `sessions/<timestamp>-<hash>/` is a launch bucket, not a session. Each session directory holds:

- `meta.json` — `session_id`, `title`, `working_directory`, `created_at`, `updated_at` (RFC3339 with nanoseconds), plus token counters and the model id, which deja does not index.
- `history.jsonl` — one message per line: `id`, `role` (`User` / `Assistant` / `Tool`), `timestamp` (RFC3339Nano), `content`, `reasoning_content`, `model_ref`, `token_usage`.
- `messages.index.jsonl` — a byte-range index over `history.jsonl`; **not** a session, never listed.

`content` is a serde variant: `{"SinglePart":{"Text":{"text":"..."}}}` or `{"MultiPart":[{"Text":{...}},{"ToolCall":{...}},{"ToolResult":{...}}]}`. deja indexes the `Text` blocks of user/assistant messages as speech. `ToolCall` and `ToolResult` bodies are renderer/tool output, not what the person said, so they are left out — as is `reasoning_content`, matching the deepseek and qwen readers. A malformed line is skipped, not fatal.

The store shape was attested by Nwflower/dsh-chat-import PR #103 (`lib/sources/codearts.mjs`, `lib/convert/codearts.mjs`) and checked against a real 0.2.8 store; the synthetic fixture in `fixtures/registry/codearts/` keeps the reader honest.

**Last verified:** 2026-11-15
