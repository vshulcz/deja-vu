# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/jetbrains/docs/hi.md)

Local cross-agent session recall, AI line blame, and handoff for IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm, and Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) indexes the session transcripts Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf, and 39 other coding agents write to disk — locally in SQLite FTS5, with secrets redacted before output and no cloud service.

**[★ Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu)** · **[Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Features

- **Status bar widget**: Displays live `deja statusline` statistics for the active project on the right side of the IDE status bar. Clicking opens the `deja-vu` action popup.
- **Search AI Sessions (`Tools → deja-vu → Search AI Sessions...`)**: Queries local sessions across all 49 supported coding agents (`deja search --json`) and opens the selected transcript in a read-only Markdown editor tab.
- **Recent AI Sessions (`Tools → deja-vu → Recent AI Sessions`)**: Lists the 50 most recent sessions on the machine (`deja last 50 --json`) with speed-search filtering.
- **AI Blame Current Line (Editor context menu / `Tools → deja-vu → AI Blame Current Line`)**: Runs `deja blame <file>:<line> --json` (falling back to `<file>`) to open the AI session that wrote or modified the line under the caret.
- **Copy Cross-Agent Handoff (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: Runs `deja handoff` in the project root, copies the digest to the system clipboard, and opens it in a read-only Markdown tab.

## Installation

1. Install the `deja` CLI and wire your local coding agents:

```sh
brew install deja-vu && deja install --auto
# or without Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Install the JetBrains plugin ([`com.github.vshulcz.deja-vu` on JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)):

- **From JetBrains Marketplace**: [Install directly from plugins.jetbrains.com](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory), or open **Settings → Plugins → Marketplace** → search `deja-vu`.
- **From Disk**: **Settings → Plugins → ⚙️ → Install Plugin from Disk...** using `deja-jetbrains.zip` from [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **From the CLI**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Replace `idea` with `goland`, `pycharm`, `webstorm`, `rustrover`, or `clion` for your IDE.)*

## Building from source

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

The installable archive is written to `build/distributions/`.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 49 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## License

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
