# @vshulcz/pi-deja

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/pi/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/)

pi remembers its own sessions. When you switch between coding tools or return to a project weeks later, this extension answers the other question: what was done in the fifty-one other coding agents on this machine — Claude Code, Codex, Cursor, Gemini, OpenClaw and Hermes among them — including the months before pi was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a single local Go binary with a local BM25 index over transcripts already on disk: no LLM, no embeddings required, and no network path besides a daily release check that `DEJA_OFFLINE=1` turns off.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

```bash
pi install npm:@vshulcz/pi-deja
```

The deja binary comes with the package; a deja you installed yourself
(`brew install deja-vu`) always wins — see [Which binary](#which-binary).

`deja install pi-auto` (or `deja install --auto` for every supported agent on the machine) wires pi too, and is the shorter path if you have the
CLI: it adds deja's MCP server and writes an extension of its own. When that
extension is present this package stands down, so having both never injects
twice — and the same for omp, whose extension the installer writes elsewhere.

omp loads `pi.extensions` manifests and emits the same `before_agent_start`
event, so `pi install`'s omp counterpart picks this package up as well.

## What it does

- **Session start**: a digest of what earlier sessions in this project decided
  goes to the model with your first prompt; a notice says it arrived, and the
  footer keeps deja's status line for the rest of the session.
- **Every prompt** (`before_agent_start`): the prompt is matched against the
  index and, when a past session answers it, that session goes to the model
  with the prompt. Silence is the common case.
- **After a failed command** (`tool_result` on `bash` or `powershell`): the repair this machine used the
  last time that error came up goes in beside the error, in the same turn.
- **Before an edit** (`tool_result` on a read, edit or write): what earlier
  sessions decided about the file the agent just opened.
- **After a compaction** (`session_compact`): the list of what this session was
  already shown is cleared, so the recall it lost can come back, and deja reads
  the turns the summary replaced so the next prompt carries what the agent was
  in the middle of.
- **When a session ends** (`session_shutdown`): its live stamp goes, so the next
  session's recall can answer with it.
- **`/deja <query>`**: search the history by hand.

## Which binary

In order: `DEJA_BIN`, `deja` on `PATH`, the usual install locations, and last
the copy bundled through `@vshulcz/deja-vu`.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 52 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## Privacy

`deja` reads session files where the agents wrote them and builds a local BM25 index under `~/.cache/deja/index.db` (or `DEJA_INDEX_DIR`). Secrets and tokens (`sk-*`, `ghp_*`, `AKIA*`, `Bearer`, private keys, URL credentials) are stripped as the index is built, and without optional semantic search (`deja embed`) indexing and search make no network calls. Pair with `deja install --auto` to wire all 52 coding agents on this machine with native MCP (`recall`, `fix`, `blame`, `handoff`).

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
