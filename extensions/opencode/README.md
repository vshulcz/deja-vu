# opencode-deja

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/opencode/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

opencode remembers its own sessions. When you switch between coding tools or reopen a repo months later, this plugin answers the other question: what you did in the fifty-two other coding agents on this machine — Claude Code, Codex, Cursor, Gemini, Hermes, and Zed among them — including transcripts recorded before `deja` was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a single local Go binary with a local BM25 index: no LLM, no embeddings required, and no network path besides an optional daily release check that `DEJA_OFFLINE=1` turns off.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["opencode-deja"]
}
```

That is opencode 1.x. On 2.x the list is `plugins`:

```json
{
  "plugins": ["opencode-deja"]
}
```

opencode installs the package on start. The deja binary comes with it, but a
deja you installed yourself always wins — see [Which binary](#which-binary).

`deja install --auto` wires opencode too, and is the shorter path if you have
the CLI: it adds deja's MCP server and writes a plugin of its own. Having both
is fine. This package reads what the installer left — the MCP entry in
`opencode.json`, the plugin file in `plugins/deja.js` — and contributes only
what is missing, so nothing is registered or recalled twice.

## What you get

Six tools the model can call:

| Tool | Answers |
|---|---|
| `deja_recall` | past sessions matching an error, function, file or flag |
| `deja_session` | the full digest of one past session — what was tried and decided |
| `deja_blame` | the sessions that discussed a file, before you edit it |
| `deja_fix` | what this machine ran after that same error, where it stayed fixed |
| `deja_how` | the real build/test/deploy invocation, with the flags actually used |
| `deja_remember` | store one durable decision |

And recall nobody has to ask for:

- the project's recent sessions are pushed onto the system prompt once per
  session, with a one-time toast so you know memory arrived (1.x only: a
  2.x server plugin has no channel to the TUI);
- each prompt you type gets a relevance pass of its own, silent when nothing
  matches; on a strong match a toast says which agent it came from and when
  (`deja-vu — from Codex CLI, 4 months ago: "…"`, 1.x only, at most once in
  20 minutes). A task sub-agent's turns are not searched again: the agent
  that wrote them already had the recall;
- after a tool call, a failed command gets the command this machine ran next
  after the same error, and a file action gets that file's prior decision,
  folded into the tool's output;
- before compaction the working transcript is indexed, so the session survives
  the window collapsing.

## Options

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

On 2.x:

```json
{
  "plugins": [{ "package": "opencode-deja", "options": { "autoRecall": false, "tools": true } }]
}
```

- `autoRecall` (default `true`) — the hooks above. Turn off to keep only
  the tools.
- `tools` (default `true`) — the six tools. They are skipped on their own when
  `deja install` already wired the MCP server; set this to `false` to drop them
  when you reach deja some other way.
- `bin` — path to a specific deja binary.

## Which binary

In order: `bin` from the options, `DEJA_BIN`, a `deja` on `PATH`, the usual
install locations (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`), and
only then the copy npm installed with this package. Your own `deja update` or
`brew upgrade deja-vu` wins over whatever version this package shipped with.

Without deja anywhere, the tools say so instead of reporting an empty history,
and the hooks stay quiet.

For full MCP tools (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) across all 53 coding tools when switching agents on one project, run `deja install --auto` (see [Switch Agents Without Losing Context](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Privacy

`deja` reads transcript files already on disk and maintains a local BM25 index under `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` overrides). Secrets and tokens (`sk-*`, `ghp_*`, `AKIA*`, `Bearer`, private keys, URL credentials) are redacted at index time before any snippet reaches opencode, and indexing/search make no network calls unless you run optional `deja embed`.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 53 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## License

MIT
