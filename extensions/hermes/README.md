# deja-memory for Hermes

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/)

A Hermes memory provider that answers from the coding sessions already on this
machine: Claude Code, Codex, Cursor, Hermes itself and the other agents deja
reads, including the months before anything was installed. No model, no API
key, no server. The history is indexed locally by
[deja](https://github.com/vshulcz/deja-vu), a single Go binary.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

deja has to be on PATH first:

```bash
brew install deja-vu        # or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # wires Hermes and every other supported agent on this machine
hermes plugins install deja-memory   # or from GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

or pick `deja-memory` in `hermes memory setup`, which checks for the binary
before it activates the provider.

## What it does

- Before the first turn of a session it hands Hermes the digest deja's hooks
  give Claude Code and Codex: the recent work in this project and what held.
  After that, only when what was just asked matches something earlier. Most
  turns it adds nothing.
- You see what it brought: the lines deja shows in Claude Code (the receipt
  naming the agent a memory came from, what a session start recalled, the
  build and week notes)
  ride Hermes's recall line, `🧠 deja · …`, in the interactive CLI and
  `hermes chat -q`. The model's text is the same as before. Hermes 0.17 has no
  recall line, `hermes -z` and `hermes chat -Q` print none, the TUI only
  flashes it in the status bar, and a gateway chat may be a group other
  people read, so there deja keeps its notes for a session that shows them.
- A cron job's run gets no automatic recall: when Hermes says the platform is
  `cron`, the provider adds nothing on its own, and the deja tools still answer
  when the job calls them.
- Three tools: `deja_recall` searches past sessions, `deja_fix` says what was
  run after an error last time, `deja_blame` lists the sessions that edited a
  file.
- An entry Hermes adds to MEMORY.md or USER.md is kept as a deja note too, so
  it ranks above the sessions around it.
- A compaction keeps what the session was in the middle of for the next turn,
  and the end of a session drops its live stamp.

One thing a memory provider cannot do: put the earlier fix beside a command
that just failed. That rides `transform_tool_result`, a plugin hook, and Hermes
hands a memory provider a context whose `register_hook` does nothing
(`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` writes the
hook plugin that carries it.

It never writes Hermes turns anywhere: Hermes already stores them, and deja
reads that store on its next refresh.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 53 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## Same code as `deja install`

`deja install hermes-auto` writes this provider to `~/.hermes/plugins/deja-memory`
along with the MCP server and a pre_llm_call hook plugin. The files here are
generated from the same source (`cmd/deja/install_hermes_memory.go`); the only
difference is that this copy finds deja on PATH instead of having the path
written in. `TestHermesExtensionIsTheProviderInstallWrites` fails when the two
drift.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
