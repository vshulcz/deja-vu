# `@vshulcz/magpie-deja` — Magpie Gateway Hook for deja-vu

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/magpie/docs/zh.md)

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

> Part of [**vshulcz/deja-vu**](https://github.com/vshulcz/deja-vu) — local cross-agent session memory for Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes, and 37+ more agents.

Recalls relevant coding session history from your local `deja` index into [Magpie](https://github.com/yetone/magpie) gateway requests across OpenAI (`chat`, `responses`), Anthropic (`anthropic`), and Gemini (`gemini`) protocols.

## Install

```bash
magpie plugin add @vshulcz/magpie-deja
```

`@vshulcz/magpie-deja` depends on `@vshulcz/deja-vu`, so installing the plugin brings the `deja` binary with it if you do not already have `deja` on `PATH` (via `brew install deja-vu` or `install.sh`). Run `deja index` once to index existing transcripts on disk.

## How It Works & Data Boundary

1. **Local index lookup (`deja hook-prompt --plain`):** On `onRequest`, the hook queries the local `~/.deja` index asynchronously via `deja hook-prompt --plain` (default timeout `800ms`). When a relevant session is found, the recalled excerpt is wrapped in `<deja-memory>` and appended to the current user turn — which means **recalled excerpts are sent with the outbound request to your configured upstream model provider**.
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
