# Magpie 网关钩子（deja-vu）

[English](../README.md) | 简体中文

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

> 属于 [**vshulcz/deja-vu**](https://github.com/vshulcz/deja-vu) —— 面向 Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes 等本地编程智能体的跨工具会话记忆索引（零大模型依赖、单 Go 二进制）。

将本地磁盘上的跨智能体历史会话召回自动接入 [Magpie](https://github.com/yetone/magpie) 本地 API 网关，覆盖 OpenAI 兼容与 Anthropic 兼容请求（`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`）。无论上游客户端是 Claude Code、Codex 还是 Hermes，经过网关时都能共享同一份本地记忆索引。

## 安装

先安装 `deja` 二进制并建立本地会话索引：

```bash
brew install deja-vu && deja index
# 或者通过 npm 安装：npm i -g @vshulcz/deja-vu && deja index
```

然后在 Magpie 中添加网关插件：

```bash
magpie plugin add github:vshulcz/deja-vu
```

## 工作原理

当来自本机回环地址或已认证来源的请求到达网关、且用户提示词不少于 4 个字符时，`extensions/magpie/deja.hooks.mjs` 会在 `onRequest` 阶段异步调用 `deja context`（严格限制在 150 毫秒超时内），检索当前工作目录与全机的历史会话决定，并以 `<untrusted-historical-context>` 引用块的形式前置注入到最新一条用户消息中。若未安装 `deja` 或没有命中相关历史记录，请求将原样透传，不会阻塞网关响应。
