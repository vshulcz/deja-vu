# deja-vu（Orca 版）

[English](../README.md) | 简体中文

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

`deja` 的 Orca v2 `trusted-worker` 插件——为 Orca 提供跨四十八个编程工具（Claude Code、Codex CLI、Cursor、opencode、Gemini CLI、Orca 及其他四十七个智能体）的本地会话记忆，涵盖安装 `deja` 之前已经记录的历史会话。

## 安装

环境要求：需先安装 [`deja`](https://github.com/vshulcz/deja-vu#install) 并加入 `PATH`（或位于 `~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin`、`~/bin` 或 `%LOCALAPPDATA%\Programs\deja`）。

在 Orca（`>=1.4.248`）中从 `Orca → Plugins → extensions/orca` 安装本插件目录。

如需同时在 Orca 内配置原生 MCP 服务、Skill 文件、`SessionStart`、`PreCompact`、`SessionEnd` 及失败命令修复记录，再执行：

```sh
deja install orca-auto
```

（`deja install orca` 仅写入 MCP、Skill 与 `/deja` 命令，不包含生命周期钩子；`deja install orca-auto` 或 `deja install --auto` 会安装完整钩子集。）

如果 `deja install orca-auto` 已经向 Orca 的 `settings.json` 写入了原生 `UserPromptSubmit` 钩子，本插件会自动退让，避免在同一轮对话中重复注入 `<deja-recall>` 上下文。

## 插件内容

- **钩子（Hooks）**
  - `agent.prompt.before`：解析当前工作区路径，在 Orca 的 2 秒预算内（`1.3s` 超时、失败放行）向提示词注入相关 `<deja-recall>` 历史上下文。
  - `agent.tool.before`：在启动 `Agent` 或 `Task` 子智能体时，仅向 `input.prompt` 追加 `<deja-recall>` 上下文，不修改 `Bash`、`Read`、`Edit` 等其他工具参数。
  - `agent.tool.after`：在 Orca 宿主 API 中为只读观测事件（不等待异步返回），因此失败命令后的 `fix` 注入由 `deja install orca-auto` 负责提供。
- **命令（Commands）**
  - `deja.orient`（`deja: Orient on workspace`）：针对当前工作区执行 `deja hook-context --plain` 并弹出摘要通知。
  - `deja.recall`（`deja: Recall past sessions`）：执行 `deja search --limit 5 -- <query>`（未传入查询时执行 `deja last -n 5`）并展示检索结果。

## 许可证

[MIT](../LICENSE)
