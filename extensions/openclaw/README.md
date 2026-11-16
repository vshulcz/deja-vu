# @vshulcz/openclaw-deja

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S · **13.6 s** to index 19,195 sessions · **half the tokens** of agentmemory) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

OpenClaw remembers its own sessions. When you move work between interactive terminals and background OpenClaw channels, this plugin answers the other question: what was done in the forty-seven other coding agents on this machine — Claude Code, Codex, Cursor, Gemini and Zed among them — including the months before OpenClaw was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a single local Go binary with a local BM25 index over transcripts already on disk: no LLM, no embeddings required, and no network path besides a daily release check that `DEJA_OFFLINE=1` turns off.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

```bash
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

The deja binary comes with the package; a deja you installed yourself
(`brew install deja-vu`) always wins — see [Which binary](#which-binary).

`deja install openclaw-auto` (or `deja install --auto` for every supported agent on the machine) wires OpenClaw too, and is the shorter path if you
have the CLI: it adds deja's MCP server and writes a plugin of its own. Having
both is fine: this package reads what the installer wrote and contributes only
what is missing.

## What it does

- **At the start of a session** (`agent:bootstrap`): what this project
  settled, once per session, in the Project Context. It needs no
  `allowConversationAccess` and runs under `openclaw agent --local` too.
- **Before each turn** (`before_prompt_build`): the prompt is matched against
  the index and, when a past session answers it, that session goes in front of
  the model. Silence is the common case. OpenClaw 2026.8.1 and later run this
  hook only with `hooks.allowConversationAccess: true` on the plugin's entry
  (see Config); without it the plugin says so in the gateway log and in `/deja`.
- **Before a compaction** (`before_compaction`): deja reads the turns about to
  be summarised from the session file, so the next turn carries what the agent
  was in the middle of.
- **At the end of a session** (`session_end`): the session's live stamp goes,
  so the next session's recall can answer with it.
- **After a tool** (tool result middleware): a read or an edit gets what past
  sessions decided about that file, and a failed command gets what this machine
  ran after the same error before, beside the result the model reads.
- **Tools**: `deja_recall` (search the history), `deja_fix` (what was run after
  this error before), `deja_blame` (which sessions touched a file and what they
  concluded).
- **`/deja <query>`**: the same search, answered to you rather than the model.

In a 1:1 chat with you on a channel (Telegram, Slack, Discord and the rest),
a reply that was answered from your history gets one italic line under it:

```
↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja
```

It is added through `reply_payload_sending` after the transcript is written,
so the model never sees it and gets exactly the text it got before. It shows
only when all of these hold, and anything missing or unknown means no line:

- the message came in a 1:1 chat: OpenClaw said `isGroup: false`, the session
  key names no group, channel, thread or topic, and any chat type it gave is
  `direct`;
- the sender is you: `commands.ownerAllowFrom`, or the channel's `allowFrom`
  when it lists exactly one id;
- the channel sends replies whole: its streaming is set off, with no block
  streaming turned on (`streaming.block.enabled` or the older `blockStreaming`,
  on the channel or an account), e.g. `channels.telegram.streaming.mode: "off"`.
  Telegram's default preview streaming sends a reply in chunks with no final
  message to carry the line, so there deja is not even asked for it and the
  receipt stays unspent;
- the reply is the final text reply of that same run, to that same
  conversation, and OpenClaw's delivery calls the chat `direct`. A media or voice reply, or a run that ends without a reply,
  gets nothing, and the next message drops it.

No other deja note is ever sent to a channel: those still come with `/deja`.
`"receipt": false` in the plugin config turns the line off.

## Config

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": { "allowConversationAccess": true },
        "config": { "autoRecall": true, "tools": true, "receipt": true, "bin": "/opt/homebrew/bin/deja" }
      }
    }
  }
}
```

All are optional. `autoRecall: false` keeps the tools and drops the
per-turn recall; `tools: false` the reverse. `receipt: false` drops the line
under replies in your DMs.

`hooks.allowConversationAccess` is OpenClaw's grant, not the plugin's: 2026.8.1
and later run a non-bundled plugin's `before_prompt_build` only with it, and a
plugin cannot ask for it in its manifest. One command sets it:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Which binary

In order: `bin` from the config, `DEJA_BIN`, `deja` on `PATH`, the usual
install locations, and last the copy bundled through `@vshulcz/deja-vu`.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 48 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## Privacy

`deja` reads session files where the agents wrote them and builds a local BM25 index under `~/.cache/deja/index.db` (or `DEJA_INDEX_DIR`). Secrets and tokens (`sk-*`, `ghp_*`, `AKIA*`, `Bearer`, private keys, URL credentials) are stripped as the index is built, and without optional semantic search (`deja embed`) indexing and search make no network calls. Pair with `deja install --auto` to wire all 48 coding agents on this machine with native MCP (`recall`, `fix`, `blame`, `handoff`).

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
