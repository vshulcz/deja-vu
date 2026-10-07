# Hermes 的 deja-memory

[English](../README.md) | 中文

一个 Hermes 记忆提供者（memory provider），答案来自这台机器上已经存在的编程会话：Claude Code、Codex、Cursor、Hermes 自己，以及 deja 能读的其他智能体，包括装任何东西之前的那几个月。不用大模型，不用 API key，不用服务器。历史记录由 [deja](https://github.com/vshulcz/deja-vu) 在本地建索引，它是一个单独的 Go 可执行文件。

## 安装

先让 deja 出现在 PATH 里：

```bash
brew install deja-vu        # 或者：go install github.com/vshulcz/deja-vu/cmd/deja@latest
hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

也可以在 `hermes memory setup` 里选 `deja-memory`，它会先检查 deja 在不在，再启用这个提供者。

## 它做什么

- 每个会话的第一轮之前，它把 deja 的钩子平时交给 Claude Code 和 Codex 的那份摘要交给 Hermes：这个项目最近做了什么，哪些结论站住了。之后只有当刚问的问题和以前的某次工作对得上时才补充内容，大多数轮次它什么都不加。
- 三个工具：`deja_recall` 搜索过去的会话，`deja_fix` 告诉你上次遇到同一个报错之后运行了什么，`deja_blame` 列出改过某个文件的会话。
- Hermes 往 MEMORY.md 或 USER.md 里加的条目，也会存成一条 deja 笔记，排序时排在周围那些会话前面。
- 压缩时保留会话正在做的事，交给下一轮；会话结束时撤掉它的在线标记。

有一件事记忆提供者做不到：在刚失败的命令旁边放上以前的修复。那要靠插件钩子 `transform_tool_result`，而 Hermes 交给记忆提供者的上下文里 `register_hook` 什么都不做（`plugins/memory/__init__.py`，0.17.0）。`deja install hermes-auto` 写入的钩子插件负责这一项。

它从不把 Hermes 的对话写到别处：Hermes 自己已经存了，deja 下次刷新时会读那个存储。

## 和 `deja install` 是同一份代码

`deja install hermes-auto` 会把这个提供者写到 `~/.hermes/plugins/deja-memory`，同时写入 MCP 服务器和一个 pre_llm_call 钩子插件。这里的文件由同一份源码生成（`cmd/deja/install_hermes_memory.go`），唯一的区别是：这一份从 PATH 里找 deja，而不是把路径写死。两份一旦不一致，`TestHermesExtensionIsTheProviderInstallWrites` 就会失败。
