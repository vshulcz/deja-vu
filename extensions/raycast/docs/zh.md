# deja-vu Raycast 扩展

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

在 Raycast 中搜索并恢复本地所有编程智能体历史会话，覆盖 Claude Code、Codex、Cursor、OpenCode、Gemini CLI 及另外 47 款工具。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 前置要求

安装 [`deja-vu`](https://github.com/vshulcz/deja-vu)：

```sh
brew install deja-vu && deja install --auto
```

## 命令

- **Search Sessions**：在 Mac 上对所有编程智能体会话执行全文搜索（`deja search --json --limit 25 -- <query>`），并将精确匹配与最相关候选分开显示。
- **Recent Sessions**：浏览全部已安装智能体最近的 100 条会话（`deja last 100 --json`），带元数据预览（`Agent`、`Project`、`Started`、`Updated`、`Session ID`）。

## 操作

- **Show Session**：直接在 Raycast 内阅读已脱敏的会话对话（`deja show <id> --json --limit 200`）。
- **Resume in Terminal**：在 `Terminal.app` 中运行该智能体的原生恢复命令（`deja resume <id>`）。
- **Copy Resume Command**、**Copy Session ID** 与 **Open Project Folder**。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 52 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 本地开发

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
