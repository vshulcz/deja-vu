# deja for Kimi Code

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/kimi/docs/hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S · **13.6 s** to index 19,195 sessions · **half the tokens** of agentmemory) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/) · **[★ Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu) indexes the session files coding
agents already write to disk — Claude Code, Codex, Cursor, opencode and
forty-four more — and answers from them. This plugin brings that index into
Kimi Code: recall arrives with the prompt, and the agent can search history
itself.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

That form pins the latest release and records where it came from, which is what
Kimi's update check reads. There is also a `kimi-deja.zip` release asset — about
10 KB instead of the whole repository — for a marketplace entry or an install
that should not pull the whole project.

Install the binary if you do not have it (and optionally wire the other agents on this machine):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Plugins load from the managed copy, so run `/reload` or `/new` after installing.

## Updates

Kimi only notifies about updates for plugins installed from its own
marketplace. For a repository install, `/plugins install` again pulls the
current release, and `deja doctor` says when the copy you have is behind the
one this deja ships:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## What you get

- **The hooks `deja install kimi-auto` writes.** On the first prompt of a
  session, what this project settled (`deja hook-context --once`). On every
  prompt, recall for what was just asked (`deja hook-prompt`); Kimi appends it
  to the turn, and nothing when nothing matches. Before a compaction, the turns
  about to be folded are read from the session, so the next prompt carries what
  the agent was in the middle of (`deja hook-precompact`). At `SessionEnd`, the
  session's live stamp goes (`deja hook-session-end`).
- **Tools.** The plugin declares `deja mcp` as an MCP server: one `deja` tool
  with a mode of `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` or `handoff`.
- **`/deja:recall <query>`** to search history directly.
- **The `deja-history` skill**, loaded at session start, so the agent knows to
  look before re-debugging something.

## Which binary

In order: `DEJA_BIN`, then the deja you installed yourself (`~/.local/bin`,
`/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), then `deja` on `PATH`. Your
own `deja update` or `brew upgrade deja-vu` wins over anything a plugin release
pinned.

## If you also ran `deja install kimi`

The CLI writes the same MCP server into `~/.kimi-code/mcp.json` and the same
hook into `config.toml`. Kimi runs a plugin's server under its own namespace
(`plugin-deja:deja`), so both would run: every tool listed twice, recall
appended twice.

This plugin checks for the installer's wiring and stands down when it finds it —
the CLI's copy is the one `deja install` keeps current. `deja uninstall kimi`
hands ownership back to the plugin, with no reinstall in between: the check runs
when the hook fires and when the server starts, not once at install time.

`deja doctor` reports `plugin` for kimi when the plugin is what carries the
wiring, so a plugin-only machine does not read as unwired.

Two things it cannot see: an MCP entry you added by hand under a name other than
`deja` (it would run beside this one), and a hook you wrote yourself that calls
deja without the installer's marker comment.

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions; MemPalace publishes 96.6%, agentmemory 95.2%) | **58% fewer tokens** (**half the tokens** of agentmemory on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 48 coding agents (next of 7 tools: 72 s) |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## License

MIT

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
