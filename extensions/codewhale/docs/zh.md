# deja-vu（CodeWhale 版）

[English](../README.md) | 简体中文

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

面向 CodeWhale 的 Claude 兼容插件包——在对话会话中直接提供 `deja` MCP 服务（包含 `recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff` 八个工具）、`deja-history` 技能指引以及 `/deja` 斜杠命令，检索范围覆盖四十七个编程工具（包含本机其他四十六个智能体已经写入磁盘的历史会话）。

## 安装与生命周期钩子

使用前请先在本机安装 [`deja`](https://github.com/vshulcz/deja-vu#install) 命令行工具，并确保 `deja` 可在 `PATH` 中直接执行。随后在 CodeWhale 内通过仓库子目录安装该插件包：

```sh
/plugin install extensions/codewhale
```

需要注意，CodeWhale 的 Claude 插件兼容加载器只会导入 MCP 服务、Skill 文件与斜杠命令，会自动忽略清单中的 `hooks` 与 `lspServers`。如果希望在 `~/.codewhale/config.toml` 中写入原生生命周期钩子，在每次提交消息前自动召回上下文、在调用编辑前提醒文件历史决策、并在命令失败后注入本机曾用过的修复方案（`message_submit`、`tool_call_before`、`tool_call_after`、`session_end`），请继续执行（`deja install codewhale` 仅配置 MCP 与 `tools.always_load`）：

```sh
deja install codewhale-auto
```

或者运行 `deja install --auto` 一次性检测并接入本机全部已安装的编程工具，配合 MCP `handoff` 工具在不同智能体之间直接交接未完成的工作；所有索引均保存在本地 `~/.cache/deja/index.db`（BM25 索引，可用 `DEJA_INDEX_DIR` 覆盖），并在建立索引时自动脱敏常见密钥与令牌。

## 插件包目录说明

- `.claude-plugin/plugin.json`：插件包元数据定义（`name: deja-vu`）
- `.mcp.json`：`deja mcp` 标准输入输出（stdio）MCP 服务端配置
- `skills/deja-history/SKILL.md`：指导模型何时及如何调用 `deja` 各检索模式的技能文件
- `commands/deja.md`：用于手动检索历史会话的 `/deja` 斜杠命令定义

## 许可证

[MIT](../LICENSE)
