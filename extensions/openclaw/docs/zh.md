# @vshulcz/openclaw-deja

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

OpenClaw 记得自己的会话。这个插件回答的是另一个问题：这台机器上 Claude Code、Codex、Cursor、Gemini、Zed 等其他五十一个编程智能体里做过什么，包括装 OpenClaw 之前的那几个月。完全依赖单文件 Go 二进制 `deja` 与本地 BM25 索引在本地运行，无需额外 LLM 或嵌入模型（设置 `DEJA_OFFLINE=1` 可关闭每日检查）。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

如果系统里还没有装 `deja`，推荐一并安装：

```sh
brew install deja-vu
```

若本机已通过 `deja install openclaw-auto` 或 `deja install --auto` 写入过原生集成包，扩展会自动避让，不重复挂载工具或注入两份记忆。

## 核心能力

- **`agent:bootstrap`（含 `openclaw agent --local`）**：会话启动时注入该目录过往已经确定的背景结论。
- **`before_prompt_build`（配合 OpenClaw `2026.8.1` 与 `hooks.allowConversationAccess: true`）**：每轮提问前检索最相关的历史记录与排障经过。
- **`before_compaction`**：上下文发生压缩前记录当前任务、定论与命令执行结果，避免在摘要阶段丢失关键参数。
- **`session_end`**：更新索引并保留当前会话记录。
- **工具结果中间件**：当命令失败或读取/修改关键文件时，随手附上过往相似报错的解法或该文件的早期决定。
- **`deja_recall` / `deja_fix` / `deja_blame` 工具**：供智能体主动检索历史、查命令修法与文件归因。
- **`/deja <query>` 命令**：在消息框中直接关键词检索全部已索引工具的历史会话。

### 私聊回执（Direct-message receipt）

在符合条件的私聊回复底部，扩展会经由 `reply_payload_sending` 附带一行斜体来源回执，例如：

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

回执仅在同时满足以下全部条件时才会附加：

1. 当前会话为非群组私聊（`isGroup: false` / `direct`）。
2. 发送方符合所有权许可（`commands.ownerAllowFrom` / `allowFrom`）。
3. 分块流式回写处于关闭状态（`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`）。
4. 该条输出为面向私聊用户的最终文本回复（`direct`）。

不希望显示来源回执时，在配置中设 `"receipt": false` 即可关闭。

## 配置

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

也可以直接通过 CLI 开启对话读取权限：

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## 使用哪份二进制

查找顺序：`bin` 配置项 -> `DEJA_BIN` -> `PATH` -> 常见安装路径 -> `@vshulcz/deja-vu` 包附带副本。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 52 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 隐私

索引保存在 `~/.cache/deja/index.db`（可用 `DEJA_INDEX_DIR` 修改），在建索引时自动清理 `sk-*`、`ghp_*`、`AKIA*` 与 `Bearer` 等凭据。只有明确运行 `deja embed` 并配置外部模型时才会出网。一键为本机 52 款编程智能体部署完整集成请运行 `deja install --auto`。

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
