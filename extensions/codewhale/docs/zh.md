# deja-vu（CodeWhale 版）

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

面向 CodeWhale 的 Claude 兼容插件包——在对话会话中直接提供 `deja` MCP 服务（包含 `recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff` 八个工具）、`deja-history` 技能指引以及 `/deja` 斜杠命令，检索范围覆盖五十二个编程工具（包含本机其他五十一个智能体已经写入磁盘的历史会话）。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

前置要求：`PATH` 中已安装 [`deja`](https://github.com/vshulcz/deja-vu#install)。

在 CodeWhale 中通过 `/plugin install` 从仓库子目录（`extensions/codewhale`）安装此插件包。

由于 CodeWhale 的 Claude 插件兼容加载器会导入 MCP 服务、技能和斜杠命令，但会忽略 `hooks` 和 `lspServers`，如果还需要把原生 CodeWhale 生命周期钩子（`message_submit`、`tool_call_before`、`tool_call_after`、`session_end`）写入 `~/.codewhale/config.toml`，请运行 `deja install codewhale-auto`（或 `deja install --auto`）（单独运行 `deja install codewhale` 仅配置 MCP 与 `tools.always_load`）：

```sh
deja install codewhale-auto
```

## 插件包内容

- `.claude-plugin/plugin.json`：插件元数据（`name: deja-vu`）
- `.mcp.json`：`deja mcp` stdio 服务配置
- `skills/deja-history/SKILL.md`：`deja` 各模式的技能说明
- `commands/deja.md`：`/deja` 斜杠命令

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 50 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

[MIT](../LICENSE)
