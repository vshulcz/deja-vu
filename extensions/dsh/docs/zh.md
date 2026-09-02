# dsh-deja

[English](../README.md) | 中文

DeepSeek Harness 自带的 `session-query` 已经可以检索它自己的会话。这个插件回答的是另一个问题：你在这台机器上**其他**智能体里做过什么。

deja 索引 Claude Code、Codex、Cursor、opencode、Antigravity、Grok Build、Kimi、Cline、Zed 等二十一个智能体本来就写在磁盘上的会话文件。不需要提前录制：历史已经在那里，包括安装 deja 之前的那些月份。如果你上周才转到 dsh，上周之前做过的事仍然可以在 dsh 里查到。

## 安装

```sh
dsh plugin --profile web add dsh-deja
```

插件调用 `deja` 可执行文件。它作为依赖随插件一起安装；如果 `PATH` 上已有 `deja` 就直接使用，`DEJA_BIN` 可覆盖两者。

装了 CLI 的话，`deja install --auto` 也会把 dsh 接好——写入 deja 的 MCP 服务端和 `/deja` 命令——那条路更短。
两边都装也没问题：插件会检查 `$DSH_HOME/plugins/deja/` 下安装器写了什么，只补缺的部分，工具和命令不会注册两次。

## 提供的能力

六个模型可调用的工具：

| 工具 | 回答什么 |
|---|---|
| `deja_recall` | 匹配报错文本、函数名、文件路径或命令行参数的历史会话。 |
| `deja_session` | 单个历史会话的完整摘要，用于需要当时的判断依据时。 |
| `deja_blame` | 在修改或删除某个文件之前，讨论过这个文件的历史会话。 |
| `deja_fix` | 这台机器上同样的报错之后跑过什么。 |
| `deja_how` | 构建、测试或部署在这台机器上真实的执行命令。 |
| `deja_remember` | 把一条已确定的决定存下来，供以后召回。 |

一个命令：`/deja <要查什么>`。

以及默认开启的自动召回：每一步之前，插件会问 deja 这台机器的历史能否回答当前提问，并把结果加入运行时上下文。多数情况下它保持沉默，只有确实有内容时才开口。

```yaml
- insert:
    - id: deja
      name: dsh-deja
      config:
        autoRecall: false   # 只保留工具和 /deja
```

## 接入方式

召回通过 `ctx.systemPrompt.context` 注入，该回调在每次 assembly 时求值。另一种看起来可行的做法——在 `agent/pre-step` 中间件里往这一步的消息列表里插入一条消息——插件能加载、回合也能正常结束，但内容到不了模型：该 waterfall 中后续的监听器会基于 payload 重建返回值，插入的消息被丢弃且没有任何报错。这一点是通过读取 dsh 0.1.1-rc.2 实际发出的请求确认的。

## 不做什么

不用大模型，不用向量嵌入，不联网。索引是本地 BM25，建立在本来就存在的文件之上，一次查询约一毫秒，数据不离开本机。密钥在建立索引时被脱敏。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。
