# @vshulcz/pi-deja

[English](../README.md) | 中文

pi 记得自己的会话。这个扩展回答的是另一个问题：这台机器上 Claude Code、Codex、Cursor、Gemini、OpenClaw、Hermes 等其他三十三个编程智能体里做过什么，包括装 pi 之前的那几个月。

扩展运行 [deja](https://github.com/vshulcz/deja-vu)，一个本地 Go 可执行文件，索引这些智能体本来就写在磁盘上的会话记录。不用大模型，不用向量嵌入，除非你主动要求，否则不走网络。

## 安装

```bash
pi install npm:@vshulcz/pi-deja
```

deja 可执行文件随包一起提供；你自己装的 deja（`brew install deja-vu`）永远优先——见[用哪个可执行文件](#用哪个可执行文件)。

装了 CLI 的话，`deja install pi-auto` 也会把 pi 接好，那条路更短：它写入 deja 的 MCP 服务端，并自己写一个扩展。那个扩展存在时，这个包会主动让位，所以两边都装也不会注入两次——omp 同理，安装器把它的扩展写在别处。

omp 读取 `pi.extensions` 清单，并发出同样的 `before_agent_start` 事件，所以 `pi install` 在 omp 那边的对应命令同样能装上这个包。

## 做什么

- **会话开始**：在你的第一条提问之前，把这个项目里先前会话定下的内容做成摘要送给模型；回执显示在页脚。
- **每条提问**（`before_agent_start`）：把提问拿去和索引比对，如果某次历史会话能回答它，那次会话就随提问一起送给模型。多数情况下是沉默。
- **命令失败之后**（`tool_result`）：上次同样的报错出现时这台机器用的修法，在同一回合里与报错一起给出。
- **修改文件之前**（读取时的 `tool_result`）：先前会话对智能体刚打开的这个文件定下了什么。
- **压缩之后**（`session_compact`）：清空这次会话已经看过的清单，于是被压缩丢掉的召回可以重新出现。
- **`/deja <要查什么>`**：手动检索历史。

## 用哪个可执行文件

顺序是：`DEJA_BIN`、`PATH` 上的 `deja`、常见安装位置，最后才是通过 `@vshulcz/deja-vu` 捆绑的那份。

## 隐私

deja 在智能体本来写入的位置读取会话文件，并在 `~/.cache/deja`（或 `DEJA_INDEX_DIR`）下建立本地索引。建立索引时密钥和令牌会被脱敏，索引和检索都不发起网络请求。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
