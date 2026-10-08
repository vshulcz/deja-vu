# @vshulcz/openclaw-deja

[English](../README.md) | 中文

OpenClaw 记得自己的会话。这个插件回答的是另一个问题：这台机器上 Claude Code、Codex、Cursor、Gemini、Zed 等其他三十九个编程智能体里做过什么，包括装 OpenClaw 之前的那几个月。

插件运行 [deja](https://github.com/vshulcz/deja-vu)，一个本地 Go 可执行文件，索引这些智能体本来就写在磁盘上的会话记录。不用大模型，不用向量嵌入，除非你主动要求，否则不走网络。

## 安装

```bash
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

deja 可执行文件随包一起提供；你自己装的 deja（`brew install deja-vu`）永远优先——见[用哪个可执行文件](#用哪个可执行文件)。

装了 CLI 的话，`deja install openclaw-auto` 也会把 OpenClaw 接好，那条路更短：它写入 deja 的 MCP 服务端，并自己写一个插件。两边都装也没问题：这个包会读取安装器写下的内容，只补缺的部分。

## 做什么

- **会话开始时**（`agent:bootstrap`）：这个项目定下的事，每个会话一次，放在 Project Context 里。不需要 `allowConversationAccess`，`openclaw agent --local` 下也会运行。
- **每回合之前**（`before_prompt_build`）：把提问拿去和索引比对，如果某次历史会话能回答它，那次会话就送到模型面前。多数情况下是沉默。OpenClaw 2026.8.1 及以后只有在插件条目上设了 `hooks.allowConversationAccess: true` 时才运行这个钩子（见配置）；没设时插件会在网关日志和 `/deja` 里说明。
- **压缩之前**（`before_compaction`）：deja 从会话文件里读出要被总结的回合，下一回合就带上智能体正在做的事。
- **会话结束时**（`session_end`）：撤掉会话的在线标记，下一个会话的召回就能用上它。
- **工具**：`deja_recall`（检索历史）、`deja_fix`（这个报错之后上次跑了什么）、`deja_blame`（哪些会话动过某个文件、当时得出了什么结论）。
- **`/deja <要查什么>`**：同样的检索，结果给你看，而不是给模型。

## 配置

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": { "allowConversationAccess": true },
        "config": { "autoRecall": true, "tools": true, "bin": "/opt/homebrew/bin/deja" }
      }
    }
  }
}
```

三项都是可选的。`autoRecall: false` 保留工具、去掉每回合的召回；`tools: false` 则相反。

`hooks.allowConversationAccess` 是 OpenClaw 的授权，不是插件的配置：2026.8.1 及以后，非内置插件的 `before_prompt_build` 只有在它为 true 时才会运行，插件也无法在 manifest 里申请。一条命令即可设置：

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## 用哪个可执行文件

顺序是：配置里的 `bin`、`DEJA_BIN`、`PATH` 上的 `deja`、常见安装位置，最后才是通过 `@vshulcz/deja-vu` 捆绑的那份。

## 隐私

deja 在智能体本来写入的位置读取会话文件，并在 `~/.cache/deja`（或 `DEJA_INDEX_DIR`）下建立本地索引。建立索引时密钥和令牌会被脱敏，索引和检索都不发起网络请求。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
