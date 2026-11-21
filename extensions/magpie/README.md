# `@vshulcz/magpie-deja` — Magpie Gateway Hook for deja-vu

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/hi.md)

[![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

Part of **[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/)

> Cross-agent session memory for Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes, and fifty-one other coding agents in total — injected at the Magpie gateway with zero LLM dependencies.

When you route multiple coding agents through a gateway, each client tracks only its own thread and loses context whenever you switch tools or start a fresh session. `@vshulcz/magpie-deja` recalls relevant prior sessions from your local `deja` BM25 index during [Magpie](https://github.com/yetone/magpie) gateway requests across OpenAI (`chat`, `responses`), Anthropic (`anthropic`), and Gemini (`gemini`) protocols.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

```bash
magpie plugin add @vshulcz/magpie-deja
```

`@vshulcz/magpie-deja` depends on `@vshulcz/deja-vu`, so installing the plugin brings the `deja` binary if you do not already have `deja` on `PATH` (via `brew install deja-vu` or `install.sh`). Run `deja index` once to index existing transcripts on disk, or run `deja install --auto` to also wire native MCP tools (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) and lifecycle hooks across all 52 supported coding agents on this machine:

```bash
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

If a client already injects `<deja-memory>` or `<deja-recall>` through native hooks wired by `deja install`, the gateway hook detects those markers and skips duplicate injection.

## How It Works & Data Boundary

1. **Local index lookup (`deja hook-prompt --plain`):** On `onRequest`, the hook queries the local BM25 index under `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` overrides) asynchronously via `deja hook-prompt --plain` (default timeout `800ms`). When a relevant session is found, the recalled excerpt is wrapped in `<deja-memory>` and appended to the current user turn — which means **recalled excerpts are sent with the outbound request to your configured upstream model provider**.
2. **Strict project scoping (`ctx.cwd`):** By default (`allowUnscoped: false`), the hook requires a non-empty client working directory (`ctx.cwd`) and never falls back to `process.cwd()` (the Magpie gateway process's own directory). `deja hook-prompt` filters the index strictly to sessions recorded in that project so unrelated repositories' history is not mixed in.
3. **Origin gate (`recallRemote: false`):** Only local requests (`ctx.origin === "local"`) and authenticated owner requests (`ctx.origin === "magpie-key"`) trigger recall unless `recallRemote: true` is explicitly enabled.
4. **Two-layer secret scrubbing:** `deja` redacts common secret and token formats (`sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, URL credentials, `api_key=...`) at index time, and `deja.hooks.mjs` runs a second regex pass (`maskSecrets`) before attaching `<deja-memory>` to the request.
5. **Duplicate guard & sticky turn cache:** Requests that already carry native `deja` recall markers (`<deja-memory`, `<deja-recall>`, `[deja —`) are skipped. Recalled blocks are cached per conversation turn in memory so multi-turn conversations and tool loops keep earlier turns byte-identical, preserving the upstream provider's prompt prefix cache.

## Options

Configure in Magpie's plugin options for `@vshulcz/magpie-deja`:

| Option | Default | Description |
|---|---|---|
| `requestTimeoutMs` | `800` | Subprocess timeout in milliseconds (`50`–`5000`). |
| `maxBytes` | `4000` | Maximum UTF-8 byte budget for each `<deja-memory>` block (`256`–`32768`). |
| `recallRemote` | `false` | Also recall on `remote` / `gateway-key` requests. |
| `allowUnscoped` | `false` | Allow global recall when the client does not send `ctx.cwd`. |
| `cwd` | `""` | Explicit fallback project directory when `ctx.cwd` is absent. |
| `skipAgents` | `[]` | Agent names (`ctx.agent`) to skip (for example `["claude-code"]` if native hooks are already active). |
| `bin` | `""` | Explicit path to the `deja` binary. |

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 52 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
