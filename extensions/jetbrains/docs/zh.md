# deja-vu JetBrains 全家桶插件（IntelliJ IDEA、GoLand、PyCharm、WebStorm、RustRover）

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | 简体中文

面向 IntelliJ IDEA、GoLand、PyCharm、WebStorm、RustRover、CLion、PhpStorm 与 Rider（`2024.2+`）的本地跨智能体会话检索、代码行溯源（AI Blame）与上下文交接插件。

[deja-vu](https://github.com/vshulcz/deja-vu) 会在本地索引 Claude Code、Codex、OpenCode、Cursor、Gemini CLI、Junie、GitHub Copilot、Aider、Windsurf 等四十三款编程智能体保存在磁盘上的会话记录——单个 Go 二进制文件与本地 SQLite FTS5 驱动，输出前自动脱敏密钥与凭据，无需联网或配置任何云端 API 密钥。

## 核心功能

- **底部状态栏组件（Status Bar Widget）**：在 IDE 底部状态栏实时展示当前项目的 `deja statusline` 索引与记忆召回统计；点击即可弹出 `deja-vu` 快捷操作菜单。
- **跨智能体搜索会话（`Tools → deja-vu → Search AI Sessions...`）**：检索本机所有四十三款编程工具的历史会话记录（`deja search --json`），并在只读 Markdown 标签页中打开完整对话上下文。
- **浏览最近会话（`Tools → deja-vu → Recent AI Sessions`）**：快速列出最近五十条 AI 编程会话并支持键盘即时过滤。
- **代码行 AI 溯源（编辑器右键菜单 / `AI Blame Current Line`）**：对光标所在的文件与行号执行 `deja blame <file>:<line> --json`，定位并打开最初编写或修改该行代码的智能体会话。
- **复制跨工具交接摘要（`Copy Cross-Agent Handoff`）**：为当前工程生成精简的 `deja handoff` 交接摘要，自动写入系统剪贴板并在只读标签页中预览，方便无缝切换到另一个终端或 IDE 智能体继续工作。

## 快速安装

1. 安装 `deja` 命令行工具并自动接入本机智能体：

```sh
brew install deja-vu && deja install --auto
# 或者无需 Homebrew 直接安装：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. 安装 JetBrains 插件（插件 ID：`com.github.vshulcz.deja-vu`）：

- **在 IDE 内安装**：打开 **Settings → Plugins → Marketplace** 搜索 `deja-vu`，或通过 **Install Plugin from Disk...** 选择 [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) 中的 `deja-jetbrains.zip`。
- **从命令行安装**：

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

（根据你使用的 IDE，可将 `idea` 替换为 `goland`、`pycharm`、`webstorm`、`rustrover` 或 `clion`。）

## 本地源码构建

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

构建产物位于 `build/distributions/` 目录下。

## 开源许可

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
