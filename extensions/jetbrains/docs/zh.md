# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

为 IntelliJ IDEA、GoLand、PyCharm、WebStorm、RustRover、CLion、PhpStorm 与 Rider（`2024.2+`）提供本地跨智能体会话检索、AI 代码行溯源（AI Line Blame）与跨智能体交接。

[deja-vu](https://github.com/vshulcz/deja-vu) 会在本地 SQLite FTS5 中索引 Claude Code、Codex、OpenCode、Cursor、Gemini CLI、Junie、GitHub Copilot、Aider、Windsurf 以及其他 39 款编程智能体写入磁盘的会话记录，输出前自动脱敏，不依赖任何云服务。

**[文档](https://vshulcz.github.io/deja-vu/zh/)** · **[评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 功能特性

- **状态栏小组件**：在 IDE 状态栏右侧实时显示当前项目的 `deja statusline` 统计信息，点击即可打开 `deja-vu` 操作弹窗。
- **检索 AI 会话（`Tools → deja-vu → Search AI Sessions...`）**：跨全部 50 款受支持的编程智能体检索本地会话（`deja search --json`），并在只读 Markdown 编辑器标签页中打开所选会话记录。
- **最近 AI 会话（`Tools → deja-vu → Recent AI Sessions`）**：列出本机最近 50 条会话（`deja last 50 --json`），支持实时快速搜索过滤。
- **AI 当前行溯源（编辑器右键菜单 / `Tools → deja-vu → AI Blame Current Line`）**：运行 `deja blame <file>:<line> --json`（若无精确行匹配则回退至 `<file>`），打开编写或修改光标所在行的 AI 会话。
- **复制跨智能体接力摘要（`Tools → deja-vu → Copy Cross-Agent Handoff`）**：在项目根目录运行 `deja handoff`，将摘要复制到系统剪贴板并在只读 Markdown 标签页中打开。

## 安装

1. 安装 `deja` CLI 并接入本地编程智能体：

```sh
brew install deja-vu && deja install --auto
# 或不使用 Homebrew：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. 安装 JetBrains 插件（[JetBrains Marketplace 上的 `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)）：

- **从 JetBrains Marketplace 安装**：[直接在 plugins.jetbrains.com 安装](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)，或打开 **Settings → Plugins → Marketplace** 搜索 `deja-vu`。
- **从本地磁盘安装**：从 [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) 下载 `deja-jetbrains.zip`，在 **Settings → Plugins → ⚙️ → Install Plugin from Disk...** 中选择安装。
- **通过命令行安装**：

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(根据所用 IDE，可将 `idea` 替换为 `goland`、`pycharm`、`webstorm`、`rustrover` 或 `clion`。)*

## 从源码构建

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

生成的安装包位于 `build/distributions/`。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 50 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
