# deja for Devin

[English](https://github.com/vshulcz/deja-vu#readme) | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.hi.md)

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

A Devin plugin that gives the agent your own past sessions as memory: what you
worked on before, in this project and others, from Devin CLI and from the
other coding agents on the machine.

[deja](https://github.com/vshulcz/deja-vu) indexes the session stores coding
agents already write to disk, including sessions from before it was installed,
and answers from them locally: BM25 over the transcripts, no model and no
embeddings. Credentials are redacted as the index is built.

## What the plugin adds

- The `deja` MCP server: search past sessions, open one as a digest, see the
  sessions behind a file, the commands that fixed an error before, the real
  build and test commands this machine runs.
- The `deja-history` skill, which tells the agent when to use it.
- Hooks: a digest of recent related work at session start and after a
  compaction, recall on each prompt, the file or command's prior decision
  after a tool call, the fix that worked last time after a failed `exec`, and
  a marker when the session ends.

## Install

The plugin calls the `deja` binary, which is not in the bundle:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Then install the plugin from this subdirectory of the repo:

```sh
devin plugins install vshulcz/deja-vu#devin-plugin
```

Without the binary the hooks stay silent and say once, at session start, how
to get it.

`deja install devin-auto` writes the same server and hooks into
`~/.config/devin` from the command line instead (`deja install devin` writes
the server alone), and it is the recommended path on Devin CLI: everything the
plugin does, it does natively. If both are present the plugin's hooks stand
down — they see the wiring in `~/.config/devin/config.json` and exit — so
nothing runs twice.

## Where this runs

On Devin CLI the hooks fire on every event the plugin wires — verified on
Devin CLI 3000.11.3. Devin's cloud sessions are reported to fire plugin hooks
on a smaller event set (`SessionStart` and `SessionEnd` among the ones not
delivered — the plugin docs are a closed beta and the list is not public), so
on those the opening digest and the end-of-session marker may be CLI-only;
the prompt, tool and compaction hooks work on both. Either way the MCP
server and the skill are always available.

deja reads Devin CLI's sessions from `~/.local/share/devin/cli/sessions.db`
(the older `cli_sessions.db` is still read), including the subagent runs it
keeps as side chains and the summaries its compactor writes beside the store.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (**88.9% hit@1** on the 470-question cleaned set; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 48 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy keeps them out of hooks). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

MIT, same as deja.

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
