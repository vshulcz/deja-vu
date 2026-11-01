# `@vshulcz/magpie-deja` — Magpie 网关钩子（deja-vu）

[English](../README.md) | 简体中文

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

> 属于 [**vshulcz/deja-vu**](https://github.com/vshulcz/deja-vu) —— 面向 Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes 等本地编程智能体的跨工具会话记忆索引（零大模型依赖、单 Go 二进制）。

在 [Magpie](https://github.com/yetone/magpie) 本地 API 网关的 `onRequest` 阶段从本机 `deja` 索引召回当前项目的历史会话决定，支持 OpenAI（`chat`、`responses`）、Anthropic（`anthropic`）与 Gemini（`gemini`）协议。

## 安装

```bash
magpie plugin add @vshulcz/magpie-deja
```

`@vshulcz/magpie-deja` 依赖 `@vshulcz/deja-vu`，若本机 `PATH` 中尚未安装 `deja`，安装插件时会自动拉取对应平台的二进制。首次使用前可执行一次 `deja index` 建立本地会话索引。

## 工作原理与数据边界

1. **本地索引检索与上游发送边界（`deja hook-prompt --plain`）：** 索引存储在本地 `~/.deja`；当 `onRequest` 命中与当前问题相关的历史记录时，钩子会将召回片段包裹在 `<deja-memory>` 标签中追加到当前用户消息末尾，**随请求一并发送给您配置的上游模型服务商**。
2. **严格按项目隔离（`ctx.cwd`）：** 默认配置（`allowUnscoped: false`）要求客户端请求提供非空的 `ctx.cwd`，绝不回退到网关自身的 `process.cwd()`。`deja hook-prompt` 仅在当前项目所属的历史会话中检索，防止其他仓库的上下文串入请求。
3. **请求来源门控（`recallRemote: false`）：** 默认仅对本机请求（`ctx.origin === "local"`）或 `magpie-key` 请求生效；除非显式开启 `recallRemote: true`，否则忽略远端共享密钥请求。
4. **双层敏感信息脱敏：** `deja` 在建立索引时已过滤常见密钥与凭证（`sk-*`、`ghp_*`、`github_pat_*`、`AKIA*`、`Bearer`、`PRIVATE KEY`、URL 凭证及 `api_key=...`），`deja.hooks.mjs` 在拼接 `<deja-memory>` 前还会执行第二道正则脱敏（`maskSecrets`）。
5. **防重复注入与多轮前缀缓存保持：** 若请求中已带有客户端原生 `deja` 钩子注入的上下文（`<deja-memory`、`<deja-recall>`、`[deja —`），网关钩子会自动跳过；每个用户轮次注入的记忆块会在会话生命周期内保持固定，保证多轮对话和工具调用循环不会破坏上游模型的 Prompt Cache。

## 配置项

可在 Magpie 的 `@vshulcz/magpie-deja` 插件配置中调整以下参数：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `requestTimeoutMs` | `800` | 调用 `deja hook-prompt` 的超时毫秒数（`50`–`5000`）。 |
| `maxBytes` | `4000` | 单个 `<deja-memory>` 块的最大 UTF-8 字节预算（`256`–`32768`）。 |
| `recallRemote` | `false` | 是否允许在 `remote` / `gateway-key` 请求上触发召回。 |
| `allowUnscoped` | `false` | 当客户端未上报 `ctx.cwd` 时是否允许全机检索。 |
| `cwd` | `""` | 当 `ctx.cwd` 缺失时使用的显式项目目录。 |
| `skipAgents` | `[]` | 需要跳过的客户端标识列表（例如已启用原生钩子的 `["claude-code"]`）。 |
| `bin` | `""` | 显式指定 `deja` 二进制文件路径。 |
