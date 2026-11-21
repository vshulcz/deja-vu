# `@vshulcz/magpie-deja` — deja-vu 的 Magpie 网关钩子

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

**[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

> 面向 Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes 及其他五十一个智能体的本机跨工具会话记忆索引（零大模型依赖、单 Go 二进制）。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> 在所有通过 [Magpie](https://github.com/yetone/magpie) 发往上游模型的对话请求里，自动附带来自本地 51 款其他编程智能体（Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes 等）的跨智能体历史记忆。

基于单文件 Go 二进制 `deja` 与本地 BM25 全文索引运行，支持 OpenAI（`chat`、`responses`）、Anthropic（`anthropic`）和 Gemini（`gemini`）协议的 Magpie 请求路径，不启用任何额外大模型与云端向量库；除明确运行的命令以及可通过 `DEJA_OFFLINE=1` 关闭的每日版本检查外，扩展自身不产生外发连接。

## 安装

```sh
magpie plugin add @vshulcz/magpie-deja
```

`@vshulcz/magpie-deja` 依赖 `@vshulcz/deja-vu`，若本机 `PATH` 中尚未安装 `deja`，安装插件时会自动拉取对应平台的二进制。首次使用前可执行一次 `deja index` 建立本地会话索引，或运行 `deja install --auto` 为本机五十二个编程工具配置原生 MCP 工具（`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）与生命周期钩子；当客户端自身已通过 `deja install` 注入记忆时，网关钩子会自动让步，不会重复注入。

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

若请求消息中已经包含 `<deja-memory>` 或 `<deja-recall>` 上下文，网关钩子会自动跳过，避免与终端智能体自身已注入的回忆叠写两次。

## 工作机制与数据边界

1. **`onRequest` 调用 `deja hook-prompt --plain`**：直接读取 `~/.cache/deja/index.db`（可通过 `DEJA_INDEX_DIR` 覆写），默认超过 `800ms` 超时直接放行，命中后在用户消息中插入 `<deja-memory>` 区块。**出站请求提示**：注入的历史片段会随这次网关请求发送给上游 LLM 提供方。
2. **严格按项目目录隔离**：默认读取 `ctx.cwd` 作为项目作用域（`allowUnscoped: false`），绝不错误回落到网关进程自己的 `process.cwd()`，仅将该路径传给 `deja hook-prompt`。
3. **默认仅限本机来源**：`recallRemote` 默认为 `false`，只在 `ctx.origin === "local"` 或 `ctx.origin === "magpie-key"` 时触发回忆。
4. **双层密钥脱敏**：构建索引时清理一次，插件注入前还通过与 `deja.hooks.mjs` 同源的 `maskSecrets` 再过滤一遍 `sk-*`、`ghp_*`、`github_pat_*`、`AKIA*`、`Bearer`、`PRIVATE KEY`、URL 凭据与 `api_key=...`。
5. **防环路与前缀缓存友好**：检测到 `<deja-memory`、`<deja-recall>` 或 `[deja —` 时不再嵌套注入，且修改放在用户请求消息末尾，不破坏系统提示词的前缀缓存（prompt prefix cache）。

## 配置项

| 选项 | 默认值 | 说明 |
|---|---|---|
| `requestTimeoutMs` | `800` | 调用 `deja` 子进程的超时时间（允许范围 `50`–`5000` 毫秒） |
| `maxBytes` | `4000` | 单次注入记忆的最大字节数（允许范围 `256`–`32768`） |
| `recallRemote` | `false` | 是否对非本地来源的网关请求启用召回 |
| `allowUnscoped` | `false` | 缺少 `ctx.cwd` 时是否仍允许跨全局索引查询 |
| `cwd` | `""` | 显式覆盖当前请求的工作目录 |
| `skipAgents` | `[]` | 跳过网关重复注入的来源列表（例如 `["claude-code"]`） |
| `bin` | `""` | 指定 `deja` 可执行文件的绝对路径 |

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 52 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
