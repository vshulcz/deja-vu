# Grok Build 中的 deja

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 个会话 · token 用量为 agentmemory 的**一半**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 点赞](https://github.com/vshulcz/deja-vu)**

Grok 记得自己的会话。这个插件回答的是另一个问题：你在这台机器上的 Claude Code、Codex、Cursor、opencode、Zed 等其他四十七个智能体里做过什么——包括你安装任何东西之前的那几个月。记忆与检索由单文件 Go 二进制 `deja` 在本地完成，无 LLM、无云端嵌入（可设 `DEJA_OFFLINE=1` 关闭每日检查）。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

先安装 `deja` 二进制本身（插件包只附带扩展声明，不含原生可执行文件）：

```sh
brew install deja-vu
# 或：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

接着从当前仓库目录的 Marketplace 安装插件（官方收录提案：[xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)）：

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

或者直接用安装器自动向 `~/.grok/config.toml`（写入 `[mcp_servers.deja]`）和 `~/.grok/hooks/deja.json` 写入全部配置：

```sh
deja install --auto
```

## 安装后包含的能力

- **`deja` MCP 工具**（8 种模式：`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）
- **`deja-history` 技能（skill）**与斜杠命令 **`/deja:recall <query>`**
- **5 个生命周期钩子（`hooks/recall.mjs`）**：`SessionStart`、`UserPromptSubmit`、`PreToolUse`（通过 `updatedInput` 补充上下文）、`PostToolUse`（在 Grok 1.0.41 上于 `Bash` 后返回排障记忆）、`PreCompact` 与 `SessionEnd`

## 同时安装了两组配置也不会冲突

如果 `~/.grok/config.toml` 里已经写了 `[mcp_servers.deja]`，或者 `~/.grok/hooks/deja.json` 里已经挂载了 deja 钩子，Marketplace 插件会自动让路，避免同一条上下文注入两次。若只保留插件安装，可运行 `deja uninstall grok` 移出全局写入。

## 使用哪份二进制

插件按顺序查找 `deja`：`DEJA_BIN` -> `~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin` -> `PATH`。

## 在未找到二进制时安静退让而不报错

插件依赖 `${GROK_PLUGIN_ROOT}` 与 `.mcp.json` 启动。从 Grok Build 1.0.5 起，如果系统里还没有装 `deja`，`grok mcp doctor` 或 `grok inspect` 会报告 `handshake failed: connection closed`，装上 `deja` 后重连即可恢复。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 为 95.2%） | **token 减少 58%**（同一仓储重复任务上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 个历史会话 / 跨越 48 个编程智能体的检索仅 **0.7 ms**（7 款对比工具中第二名 72 秒） |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## ## 许可证

MIT

觉得有用？[去 GitHub 为 deja-vu 点颗星](https://github.com/vshulcz/deja-vu)。
