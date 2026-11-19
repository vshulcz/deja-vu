# deja-vu（Orca 版）

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

`deja` 的 Orca v2 `trusted-worker` 插件——为 Orca 提供跨四十九个编程工具（Claude Code、Codex CLI、Cursor、opencode、Gemini CLI、Orca 及其他四十八个智能体）的本地会话记忆，涵盖安装 `deja` 之前已经记录的历史会话。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

前置要求：`PATH` 中（或 `~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin`、`~/bin`、`%LOCALAPPDATA%\Programs\deja` 下）已安装 [`deja`](https://github.com/vshulcz/deja-vu#install)。

在 Orca（`>=1.4.248`）中添加插件目录 `extensions/orca`。

如果需要在 Orca 内启用原生 MCP、技能文件、`SessionStart`、`PreCompact`、`SessionEnd` 以及命令失败后的自动修复召回，请同时运行：

```sh
deja install orca-auto
```

（`deja install orca` 仅安装 MCP、技能与 `/deja`，不含生命周期钩子；`deja install orca-auto` 或 `deja install --auto` 会配置完整钩子集合。）

当 `deja install orca-auto` 已向 Orca 的 `settings.json` 写入原生 `UserPromptSubmit` 钩子时，本 worker 会自动退出，避免重复注入 `<deja-recall>` 上下文帧。

## 插件提供的功能

- **Hooks**
  - `agent.prompt.before`：解析当前工作区路径，并在 Orca 的 2 秒钩子预算内（`1.3s` 超时，失败跳过）向提示词注入相关 `<deja-recall>` 上下文。
  - `agent.tool.before`：在拉起 `Agent` 或 `Task` 子智能体时，仅向 `input.prompt` 追加 `<deja-recall>` 上下文（绝不修改 `Bash`、`Read`、`Edit` 等其他工具输入）。
  - `agent.tool.after`：在 Orca 宿主 API 中属于只读观察点（返回值 `next` 会被忽略且宿主不等待），因此失败命令的 `fix` 推送由 `deja install orca-auto` 提供。
- **Commands**
  - `deja.orient`（`deja: Orient on workspace`）：针对当前工作区执行 `deja hook-context --plain` 并显示摘要通知。
  - `deja.recall`（`deja: Recall past sessions`）：执行 `deja search --limit 5 -- <query>`（未提供查询词时执行 `deja last -n 5`）并展示结果。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

[MIT](../LICENSE)
