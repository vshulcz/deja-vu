# deja for Zed

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/)

[deja](https://github.com/vshulcz/deja-vu) indexes the session files that
coding agents already write to disk — Claude Code, Codex, Cursor, opencode and
forty-nine more — and answers from them. This extension serves that index to Zed's
agent panel as a context server, so a thread can search what you did before,
including work from before deja was installed.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## What the agent gets

One tool, `deja`, called with a `mode`:

| Mode | Answers |
|---|---|
| `recall` | Sessions matching an error string, function name, file path or flag. |
| `context` | The full digest of one past session, when the reasoning behind it matters. |
| `blame` | The sessions that discussed a file, before you edit or delete it. |
| `fix` | What this machine ran after that same error before. |
| `how` | The real invocation for a build, test or deploy, from what ran here. |
| `orient` | The commands past sessions ran in this project and the files they worked in. |
| `remember` | Stores one durable decision for later recall. |
| `handoff` | Another session's state, to continue its work where it stopped. |

## Install

Install the extension from Zed's extension list. On first use it asks Zed to
run `deja --version`, and when that works it runs the `deja` on Zed's PATH.
Otherwise it downloads a release build of the `deja` binary into its own
directory. An extension runs sandboxed and cannot look in the usual install
paths itself, so a deja that is not on the PATH Zed was started with has to be
named:

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` reaches Zed too, and writes the server into
`settings.json` directly — the shorter path when you have the CLI. Both use the
same id, `deja-context-server`, and Zed keys servers by id, so either order
leaves exactly one server: the installer leaves this extension's entry alone
when it finds it, and installing the extension later lands on the same key
rather than beside it. `deja uninstall zed` removes what the CLI wrote.

Indexing and search are local: no network calls, and credentials are redacted
as the index is built.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 53 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

MIT, same as deja.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
