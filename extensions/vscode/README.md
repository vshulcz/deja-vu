# deja-vu for VS Code, Cursor, Windsurf & Antigravity


[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/vscode/docs/hi.md)

**Your coding agents stop re-debugging what you already fixed.**

[deja-vu](https://github.com/vshulcz/deja-vu) indexes the session transcripts Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code and 41 other coding agents already write to disk — locally, in one Go binary, with no LLM and no background daemon.

**[Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## What this extension does

Shows live `deja statusline` state in the IDE status bar (sessions indexed across your agents, memory recalls served today, and workspace history). If `deja` is not installed yet, clicking the status bar item runs `npx -y @vshulcz/deja-vu install --auto` in a terminal to wire all detected agents on your machine.

## Quick install

```sh
brew install deja-vu && deja install --auto
# or without Homebrew:
npx -y @vshulcz/deja-vu install --auto
```

Install the status bar extension from the CLI:

```sh
code --install-extension vshulcz.deja-status
```

## Measured impact

| Benchmark | Result |
|---|---|
| LongMemEval-S retrieval accuracy (470 cleaned questions) | **97.2% R@5**, **88.9% hit@1** |
| Task token cost on repeated repository work | **58% fewer tokens**|
| In-process lexical query latency | **0.7–0.8 ms** median across 52 coding agents |
| Day-zero indexing over 19,195 real sessions | **13.6 s**, zero API keys |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## License

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
