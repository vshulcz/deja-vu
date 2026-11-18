# deja for CodeBuddy Code

[English](https://github.com/vshulcz/deja-vu#readme) | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.hi.md)

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

A CodeBuddy Code plugin that gives the agent your own past sessions as memory:
what you worked on before, in this project and others, from CodeBuddy and from
the other coding agents on the machine.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
coding agents already write to disk, including sessions from before it was
installed, and answers from them locally: BM25 over the transcripts, no model
and no embeddings. Credentials are redacted as the index is built.

## What the plugin adds

- The `deja` MCP server: search past sessions, open one as a digest, see the
  sessions behind a file, the commands that fixed an error before, the real
  build and test commands this machine runs.
- The `deja-history` skill, which tells the agent when to use it.
- Hooks: a digest of recent related work at session start, recall on each
  prompt, what past sessions settled about a file after it is read or edited,
  the fix that worked last time after a failed `Bash` or `PowerShell` command,
  a capture before compaction, and a marker when the session ends.

## Install

The plugin calls the `deja` binary, which is not in the bundle:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Then install the plugin from the CodeBuddy marketplace. Without the binary the
hooks stay silent and say once, at session start, how to get it.

`deja install codebuddy-auto` writes the same server and hooks into
`~/.codebuddy` from the command line instead (`deja install codebuddy` writes
the server alone). If both are present the plugin's hooks stand down, so
nothing runs twice.

On Windows the plugin's hooks and server are shell scripts, so they need Git
for Windows. Without it, use `deja install codebuddy-auto`, which writes hooks
that run through PowerShell.

deja reads CodeBuddy's own sessions from `~/.codebuddy/projects`
(`CODEBUDDY_CONFIG_DIR` is honoured) and WorkBuddy's from `~/.workbuddy/projects` and, for WorkBuddy AI,
`~/.workbuddy-ai/projects` (`WORKBUDDY_CONFIG_DIR` is honoured).

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (**88.9% hit@1** on the 470-question cleaned set; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 49 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy keeps them out of hooks). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## Privacy

Indexing and search are local: deja reads the transcripts in place and keeps
its index under `~/.cache/deja`. The network is used only by `deja update`,
`deja sync ssh`, the version check in `deja doctor`, `deja embed` against an
endpoint you configure (and, once it has built the semantic sidecar, each
search, which sends the query to that endpoint), and a once-a-day release check from a command typed at a
terminal (`DEJA_OFFLINE=1` turns it off). Details are in the
[repository README](https://github.com/vshulcz/deja-vu#readme); security policy in
[SECURITY.md](https://github.com/vshulcz/deja-vu/blob/main/SECURITY.md).

MIT, same as deja.

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
