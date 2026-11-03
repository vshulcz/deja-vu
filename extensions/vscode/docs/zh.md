# deja-vu VS Code、Cursor、Windsurf 与 Antigravity 状态栏扩展

[English](../README.md) | 简体中文

**别再让编程智能体重新调试你已经修好的问题。**

[deja-vu](https://github.com/vshulcz/deja-vu) 会在本地索引 Claude Code、Cursor、Codex、Windsurf、Copilot Chat、OpenCode、Cline、Roo Code 等四十三款编程智能体保存在磁盘上的会话记录——单个 Go 二进制文件运行，无需调用大语言模型，无需 API 密钥，也没有常驻后台守护进程。

## 功能说明

本扩展在 VS Code、Cursor、Windsurf、Antigravity、Trae 和 Kiro 的底部状态栏右侧实时显示 `deja statusline` 状态（当前已索引的跨智能体会话总数、今日向智能体提供的记忆召回次数以及当前工作区的历史统计）。如果本机尚未安装 `deja`，点击状态栏按钮即可在终端中一键运行 `npx -y @vshulcz/deja-vu install --auto` 完成安装与自动配置。

## 快速安装

```sh
brew install deja-vu && deja install --auto
# 或者无需 Homebrew 直接运行：
npx -y @vshulcz/deja-vu install --auto
```

从命令行安装状态栏扩展：

```sh
code --install-extension vshulcz.deja-status
```

## 开源许可

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
