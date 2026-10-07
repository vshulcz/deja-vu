# Grok Build 上的 deja

[English](../README.md) | 中文

Grok 记得自己的会话。这个插件回答的是另一个问题：你在这台机器上的 Claude Code、Codex、Cursor、opencode、Zed 等其他三十七个智能体里做过什么——包括你安装任何东西之前的那几个月。

插件运行 [deja](https://github.com/vshulcz/deja-vu)，一个本地 Go 可执行文件，索引这些智能体本来就写在磁盘上的会话记录。不用大模型，不用向量嵌入，除非你主动要求，否则不走网络。

## 安装

```sh
grok plugin marketplace add xai-org/plugin-marketplace
grok plugin install deja
```

先装可执行文件——插件分发的是文件，不是运行时，也不是原生二进制：

```sh
brew install deja-vu
```

或者

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

装了 CLI 的话，`deja install --auto` 同样能接上 Grok，那条路更短：它把 `[mcp_servers.deja]` 写进 `~/.grok/config.toml`，把它的钩子写进 `~/.grok/hooks/deja.json`。两条路各自都够用：插件跑的是同样的钩子。

## 提供的能力

MCP 服务端，带着 deja 在各处提供的那一个工具：`deja`，模式可以是 `recall`、`context`、`blame`、`fix`、`how`、`orient` 或 `remember`。

`deja-history` 技能，让智能体在够不着工具时仍然知道 CLI 的用法。

`/deja:recall <要查什么>`，用于你想直接问的时候。

`deja install grok-auto` 写入的那些钩子，经由 `hooks/recall.mjs` 运行：

- `Bash` 上的 `PostToolUse`：失败的命令旁边会附上这台机器上次遇到同样错误后跑过的修复（Grok 1.0.41 会把这个钩子的上下文交给模型）。
- 会改动东西的工具上的 `PreToolUse`：派生出来的智能体通过 `updatedInput` 在提示里拿到这个项目的记忆。
- `PreCompact`：忘掉这个会话已经看过的内容，压缩之后召回可以再次发送。
- `SessionEnd`：撤掉会话的在线标记，下一个会话的召回就能用上它。
- `SessionStart` 和 `UserPromptSubmit`：Grok 会丢掉它们打印的内容，所以它们只用来预热索引、标记会话在线，什么都不输出。提问要通过 MCP 工具到达历史。

## 两边都装也没问题

`deja install grok` 和这个插件接的是同样的两样东西，而 Grok 会把两份都跑起来——每个工具列两遍，每条提问把同样的召回读两遍。所以两边各自在发现安装器的副本时主动让位：

- 当 `~/.grok/config.toml` 里有 `[mcp_servers.deja]` 时，MCP 服务端回应握手，但不列出任何工具；
- 当 `~/.grok/hooks/deja.json` 里有 deja 的钩子时，钩子直接返回、不输出任何内容。

`deja install` 写下的那份优先，因为那是它负责保持更新的副本。`deja uninstall grok` 把所有权交回插件。

## 用哪个可执行文件

顺序是：`DEJA_BIN`，然后是你自己装的 deja（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin`），最后是裸名字，于是 `PATH` 仍然说得上话。你自己的 `deja update` 或 `brew upgrade` 会盖过插件发布时冻结的任何版本。

哪里都没有 deja 时，钩子保持沉默，MCP 服务端会说明缺了什么，而不是假装历史是空的。

## 让位而不显得坏掉

`${GROK_PLUGIN_ROOT}` 在 `.mcp.json` 里和在钩子命令里一样会展开——这是在 Grok Build 1.0.5 上实测的：启动一个把自己收到的参数写回文件的插件服务端，因为 `grok mcp doctor` 和 `grok inspect` 都不报告插件的 MCP 服务端。

当 `deja install grok` 已经接好了 CLI 那一份时，这一份会回应握手但不列出任何工具。直接退出会被报成 `handshake failed: connection closed`，在插件界面里留下一个坏掉的服务端，那比留下一个闲着的更糟。

## 许可

MIT

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
