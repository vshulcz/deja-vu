# deja for Claude Code

[English](https://github.com/vshulcz/deja-vu#readme) | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/docs/readme/README.hi.md)

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

The plugin bundle Claude Code, Qwen and OpenClaw install from
this repository's marketplace. Cursor has its own, in
[`cursor-plugin/`](../cursor-plugin/). Copilot CLI installs it too but takes only the
skill (no hooks, no MCP server); `deja install copilot-auto` wires those:

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

It carries deja's MCP server, the `deja-history` skill, the `/deja` command and
the session-start, per-prompt (`UserPromptSubmit`), pre-compact (`PreCompact`), pre-tool (`PreToolUse`), post-failure (`PostToolUseFailure`) and session-end (`SessionEnd`) hooks — the same
wiring `deja install --auto` writes, packaged the way those harnesses install
things.

`deja` indexes the session transcripts of every coding agent on the machine (`forty-nine coding agents` total), across projects, and the hooks inject matching snippets from those local transcripts into the model's context (`UserPromptSubmit` recalls past sessions matching the prompt; `PreToolUse` surfaces decisions, verification commands, and co-changed files for edited paths, or pre-flight failure warnings before shell commands). Recalled transcripts are an untrusted text channel (they can contain earlier tool outputs or external content), so hook output is wrapped with a `<deja-recall>` untrusted-data framing banner and credentials are redacted at index time.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
forty-nine coding agents already write to disk, including sessions from before it
was installed, and answers from them locally: BM25 over the transcripts, no
model and no embeddings, credentials redacted as the index is built.

The binary is not in the bundle — plugins carry files, not native binaries:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
```

Having both this bundle and `deja install` is fine: the hooks here stand down
when the installer has already wired the same ones, so nothing is recalled
twice.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (**88.9% hit@1** on the 470-question cleaned set; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 49 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy keeps them out of hooks). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](../SECURITY.md).

MIT, same as deja.

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
