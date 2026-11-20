# deja-vu for CodeWhale

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/zh.md) | [繁體中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/zh-TW.md) | [日本語](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/ja.md) | [한국어](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/ko.md) | [Español](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/es.md) | [Português](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/pt.md) | [Français](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/fr.md) | [Deutsch](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/de.md) | [Русский](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/ru.md) | [Türkçe](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/tr.md) | [हिन्दी](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/hi.md)

Claude-compatible plugin bundle for CodeWhale — exposes the `deja` MCP server (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), the `deja-history` skill, and `/deja` across sessions from fifty-one AI coding tools.

![deja-vu demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Install

Requirements: [`deja`](https://github.com/vshulcz/deja-vu#install) installed on `PATH`.

Install this bundle in CodeWhale from the repository subdirectory (`extensions/codewhale`) using `/plugin install`.

Because CodeWhale's Claude plugin compatibility loader imports MCP servers, skills, and slash commands but ignores `hooks` and `lspServers`, run `deja install codewhale-auto` (or `deja install --auto`) if you also want native CodeWhale lifecycle hooks (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) wired in `~/.codewhale/config.toml` (`deja install codewhale` wires MCP and `tools.always_load` only):

```sh
deja install codewhale-auto
```

## Bundle contents

- `.claude-plugin/plugin.json`: plugin metadata (`name: deja-vu`)
- `.mcp.json`: `deja mcp` stdio server entry
- `skills/deja-history/SKILL.md`: skill instructions for the `deja` modes
- `commands/deja.md`: `/deja` slash command

## Benchmarks

| Recall | Tokens | Index & lookup |
|---|---|---|
| **97.2% R@5** on LongMemEval-S (88.9% hit@1, 470 questions) | **58% fewer tokens** (on repeated repo tasks) | **13.6 s** to index 19,195 sessions / **0.7 ms** lookup across 51 coding agents |

## Repository memory & handoff

Share vetted project decisions and dead ends through git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Entries are redacted on write, stored sorted with `merge=union` so parallel branches merge cleanly, and loaded for any teammate who clones the repository (`{"activations":{"auto":{"repo":false}}}` in the trust policy disables hook injection). [More](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Switching tools mid-task? Run `deja handoff --to <agent>` (or use `handoff` via MCP) to transfer the active session's goal, decisions, touched files, passed checks, and open work into another coding agent.

## License

[MIT](LICENSE)
