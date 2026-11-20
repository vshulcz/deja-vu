# deja for Cursor

[English](https://github.com/vshulcz/deja-vu#readme) | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.hi.md)

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

The Cursor plugin for [deja](https://github.com/vshulcz/deja-vu). It carries
deja's MCP server, the `deja-history` skill, the `/deja` command and the
`sessionStart`, `beforeSubmitPrompt`, `preToolUse`, `postToolUse`,
`postToolUseFailure`, `preCompact` and `sessionEnd` hooks: the same wiring
`deja install cursor-auto` writes.

deja indexes the session transcripts fifty coding agents already write to
disk, including sessions from before it was installed, and answers from them
locally: BM25 over the transcripts, no model and no embeddings, credentials
redacted as the index is built.

## Install deja first

The plugin does not carry the binary:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Until `deja` is on PATH (or in `~/.local/bin`, `/opt/homebrew/bin`,
`/usr/local/bin` or `~/go/bin`) the hooks do nothing and the MCP server reports
that it could not start.

The hooks and the server start through `sh`, so this plugin works on macOS and
Linux. On Windows use `deja install cursor-auto` instead.

Having both this plugin and `deja install cursor-auto` is fine: the hooks here
stand down when the installer has already wired the same ones in
`~/.cursor/hooks.json`.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (**88.9% hit@1** on the 470-question cleaned set) | **58% fewer tokens** on repeated repo tasks | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 50 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy keeps them out of hooks). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](SECURITY.md).

MIT, same as deja.
