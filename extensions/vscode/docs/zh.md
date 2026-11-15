# deja-vu（VS Code、Cursor、Windsurf 与 Antigravity 扩展）

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**让你的编程智能体不再重复排查已经解决过的问题。**

[deja-vu](https://github.com/vshulcz/deja-vu) 本地索引 Claude Code、Cursor、Codex、Windsurf、Copilot Chat、OpenCode、Cline、Roo Code 与其他 40 个编程智能体写入磁盘的历史会话——单个 Go 二进制本地运行，无 LLM 调用、无后台守护进程。

**[★ 去 GitHub 点颗星](https://github.com/vshulcz/deja-vu)** · **[文档](https://vshulcz.github.io/deja-vu/zh/guide/getting-started.html)** · **[基准评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 功能说明

在 IDE 状态栏实时展示 `deja statusline` 统计（已索引的跨智能体会话数、今日召回次数与当前工作区历史）。如果系统尚未安装 `deja`，点击状态栏条目会在终端中自动执行 `npx -y @vshulcz/deja-vu install --auto` 接通本机所有智能体。

## 快速安装

```sh
brew install deja-vu && deja install --auto
# 或不使用 Homebrew：
npx -y @vshulcz/deja-vu install --auto
```

从命令行安装状态栏扩展：

```sh
code --install-extension vshulcz.deja-status
```

## 实测效果

| 指标 | 结果 |
|---|---|
| LongMemEval-S 检索准确率（470 道清洗后题目） | **97.2% R@5**，**88.9% hit@1**（MemPalace 公布 96.6%，agentmemory 为 95.2%） |
| 重复仓库任务的 Token 消耗 | **减少 58%**——只有 agentmemory 的**一半**（`52,815` 对比冷启动 `103,443`，11 组配对测试） |
| 进程内词法查询延迟 | 跨 48 个编程智能体中位数 **0.7–0.8 ms** |
| 对 19,195 个历史会话的零配置建库耗时 | **13.6 s**，无需 API Key（7 款工具中第二名为 72 s） |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。觉得有用？[去 GitHub 为 deja-vu 点颗星](https://github.com/vshulcz/deja-vu)。
