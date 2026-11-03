# deja-vu Raycast 扩展


[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | 简体中文

在 Raycast 中全文搜索并恢复 Claude Code、Codex、Cursor、OpenCode、Gemini CLI 等全部四十三款编程智能体的本地历史会话。每一个智能体本来就会把完整的对话、执行的命令和修改过的文件记录在你的电脑磁盘上，deja 在本地建立统一索引，让你随时在 Raycast 里按关键词找回之前解决过的报错、配置决策或讨论过程，而不需要连接任何外部模型服务或后台守护进程。

## 前置条件

先在本地安装 [`deja-vu`](https://github.com/vshulcz/deja-vu) 命令行工具并建立索引：

```sh
brew install deja-vu && deja install --auto
```

如果你通过其他路径安装了二进制文件，可以在 Raycast 的扩展偏好设置中指定 `dejaPath`，扩展也会自动检查 `/opt/homebrew/bin`、`/usr/local/bin`、`~/.local/bin` 与 `~/go/bin`。

## 命令与操作说明

- **Search Sessions**：全文搜索本机所有编程智能体的历史会话（通过 `deja search --json --limit 25 -- <query>`），自动区分严格匹配结果与相关性近似结果；当搜索框为空时展示最近三十条会话。
- **Recent Sessions**：浏览最近一百条跨智能体会话（`deja last 100 --json`），并在右侧详情面板展示所属智能体、项目路径、开始时间、更新时间与会话编号。
- **Show Session**：在 Raycast 内直接阅读脱敏后的完整对话记录，内置安全引用格式，避免代码块或标题破坏排版。
- **Resume in Terminal**：解析对应智能体的恢复命令并在 `Terminal.app` 中直接打开原工作目录继续会话，同时支持复制恢复命令、复制会话编号以及在 Finder 中打开项目文件夹。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
