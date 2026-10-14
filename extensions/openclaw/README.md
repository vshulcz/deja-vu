# @vshulcz/openclaw-deja

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/openclaw/docs/zh.md)

OpenClaw remembers its own sessions. This plugin answers the other question:
what was done in the forty-one other coding agents on this machine — Claude
Code, Codex, Cursor, Gemini and Zed among them — including the months before
OpenClaw was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, no network path besides a daily release check, which DEJA_OFFLINE=1 turns off.

## Install

```bash
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

The deja binary comes with the package; a deja you installed yourself
(`brew install deja-vu`) always wins — see [Which binary](#which-binary).

`deja install openclaw-auto` wires OpenClaw too, and is the shorter path if you
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
↺ you've been here: "how should the zorbit queue drain on deploy" · Oct 4 · deja
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

## Privacy

deja reads session files where the agents wrote them and builds a local index
under `~/.cache/deja` (or `DEJA_INDEX_DIR`). Keys and tokens are stripped as
the index is built, and without semantic search (`deja embed`) indexing and
search make no network calls.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
