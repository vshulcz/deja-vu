# deja-vu for Raycast


[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/raycast/docs/hi.md)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/)

Search and resume coding agent sessions across Claude Code, Codex, Cursor, OpenCode, Gemini CLI and 46 more harnesses from Raycast.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Prerequisites

Install [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Commands

- **Search Sessions**: Full-text search across every coding agent session on your Mac (`deja search --json --limit 25 -- <query>`), separating strict matches from nearest-session relevance hits.
- **Recent Sessions**: Browse the 100 most recent sessions across all installed agents (`deja last 100 --json`) with metadata preview (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Actions

- **Show Session**: Read the redacted conversation inside Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: Run the agent's native resume command (`deja resume <id>`) in `Terminal.app`.
- **Copy Resume Command**, **Copy Session ID**, and **Open Project Folder**.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 51 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## Local development

```sh
cd extensions/raycast && npm install && npx ray develop
```

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
