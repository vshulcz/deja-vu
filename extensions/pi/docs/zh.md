# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

pi 记得自己的会话。这个扩展回答的是另一个问题：这台机器上 Claude Code、Codex、Cursor、Gemini、OpenClaw、Hermes 等其他四十九个编程智能体里做过什么，包括装 pi 之前的那几个月。记忆在本地以单文件 Go 二进制 `deja` 运行并建立 BM25 索引，无需额外 LLM、嵌入模型或云端账号；除主动运行的更新与同步命令及可用 `DEJA_OFFLINE=1` 关闭的每日版本检查外，不发起网络请求。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

```sh
pi install npm:@vshulcz/pi-deja
```

如果系统里还没有装 `deja`，扩展会按[使用哪份二进制](#使用哪份二进制)中的顺序回退到随包附带的复制，也可以自行提前安装：

```sh
brew install deja-vu
```

若本机已通过 `deja install pi-auto` 或 `deja install --auto` 写入了独立扩展，这个 npm 包会自动退让，避免在同一个会话里挂载两份工具或重复注入记忆。这套扩展机制同样兼容基于 `pi.extensions` 与 `before_agent_start` 的 `omp`。

## 扩展功能

- **会话启动（页脚状态栏）**：进入目录时在状态栏显示当前项目已索引的会话数与命中统计。
- **每轮提问（`before_agent_start`）**：按当前 prompt 检索同一项目曾做过的决定与排障经过并自动附带给模型。
- **命令失败后（`bash` / `powershell` 的 `tool_result`）**：当命令以非零退出码结束时，附上本地历史上遇到同一类报错后紧接着跑通的命令。
- **编辑与读取前（read/edit/write 的 `tool_result`）**：在修改关键文件前后提示过往会话针对该路径的改动理由。
- **上下文压缩后（`session_compact`）**：在压缩摘要落地时补回当前任务目标、关键结论与尚未收尾的命令状态。
- **会话结束（`session_shutdown`）**：刷新当前会话索引并记录结束状态。
- **`/deja <query>` 命令**：在聊天输入框里随时按关键词检索所有已索引智能体的历史会话。

## 使用哪份二进制

扩展按以下优先级查找并调用 `deja` 可执行文件：

1. 环境变量 `DEJA_BIN` 指定的路径
2. 当前 `PATH` 中的 `deja`
3. 常见用户与包管理器安装路径（`~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin` 等）
4. npm 包 `@vshulcz/deja-vu` 随附的二进制

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 隐私与安全

索引保存在 `~/.cache/deja/index.db`（可通过 `DEJA_INDEX_DIR` 修改），在构建时自动脱敏 `sk-*`、`ghp_*`、`AKIA*` 与 `Bearer` 等凭据。所有检索默认纯本地完成；仅在你显式运行 `deja embed` 并配置外部嵌入端点时才会离开本机。完整 CLI 集成（`recall`、`fix`、`blame` 与 `handoff`）可在 49 款智能体上通过 `deja install --auto` 一键启用。

## 许可证

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
