# deja-vu for CodeWhale

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/codewhale/docs/zh.md)

Claude-compatible plugin bundle for CodeWhale — exposes the `deja` MCP server (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), the `deja-history` skill, and `/deja` across sessions from forty-seven AI coding tools.

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

## License

[MIT](LICENSE)
