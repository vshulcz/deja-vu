# deja-memory for Hermes

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 个会话 · token 用量为 agentmemory 的**一半**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 点赞](https://github.com/vshulcz/deja-vu)**

一个 Hermes 记忆后端（memory provider），直接基于本机已有的编程会话历史回答问题：包括 Claude Code、Codex、Cursor、Hermes 自身以及 deja 支持读取的其他智能体，涵盖安装任何工具之前数月积累的记录。无需额外模型、API 密钥或后台服务；所有历史由单文件 Go 二进制 [deja](https://github.com/vshulcz/deja-vu) 在本地建立索引。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

需确保 `PATH` 中已可调用 `deja`：

```bash
brew install deja-vu        # 或: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # 一并接入 Hermes 与本机所有受支持的智能体
hermes plugins install deja-memory   # 或从 GitHub 安装: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

也可以在 `hermes memory setup` 中选择 `deja-memory`，它会在启用前检查 `deja` 可执行文件是否存在。

## 工作机制

- **首轮会话前注入上下文**：向 Hermes 传入 deja 钩子给 Claude Code / Codex 使用的同款项目摘要——包括本仓库近期工作与已确定的结论；此后仅在当前提问命中过往会话时才附加召回结果，大多数对话轮次不会增加额外内容。
- **清晰可见的召回提示**：在交互式 CLI 与 `hermes chat -q` 中，deja 在 Claude Code 里展示的来源回执（记忆来自哪款智能体、会话启动召回内容、构建与周报说明）会挂载在 Hermes 的召回行 `🧠 deja · …` 上，模型的原始回复保持不变。Hermes 0.17 无召回行，`hermes -z` 与 `hermes chat -Q` 也不打印该行，TUI 仅在状态栏闪现，网关聊天可能是多人可见的群聊——这些场景下 deja 会保留提示，待支持显示的交互会话中再呈现。
- **定时任务（`cron`）静默**：当 Hermes 报告平台为 `cron` 时，后端不会自动注入记忆；如果定时任务主动调用 deja 工具，工具仍正常应答。
- **三个内置工具**：`deja_recall` 检索历史会话，`deja_fix` 查询同类报错后的修复记录，`deja_blame` 列出编辑过指定文件的会话。
- **笔记提升权重**：Hermes 写入 `MEMORY.md` 或 `USER.md` 的条目也会保留为 deja 笔记，在检索排序中优先于周围普通会话。
- **压缩保留断点**：上下文压缩（compaction）时会保留会话中止前正在处理的工作以便下一轮衔接；会话结束时自动移除活跃标记。

有一项能力记忆后端本身无法完成：把历史修复直接贴在刚刚失败的命令结果旁。这依赖插件钩子 `transform_tool_result`，而 Hermes 向记忆后端传入的上下文里 `register_hook` 为空实现（`plugins/memory/__init__.py`，0.17.0）。执行 `deja install hermes-auto` 可一并安装承载该能力的钩子插件。

扩展从不额外转写 Hermes 对话轮次：Hermes 自身已经将记录落盘，deja 会在下次刷新时直接读取。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 为 95.2%） | **token 减少 58%**（同一仓储重复任务上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 个历史会话 / 跨越 48 个编程智能体的检索仅 **0.7 ms**（7 款对比工具中第二名 72 秒） |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 与 `deja install` 同源

`deja install hermes-auto` 会将此后端写入 `~/.hermes/plugins/deja-memory`，并连同 MCP 服务与 `pre_llm_call` 钩子插件一并部署。此处扩展文件与安装器出自同一份源码（`cmd/deja/install_hermes_memory.go`）；唯一差异在于本扩展从 `PATH` 查找 `deja`，而安装器会把路径直接写入。一旦两者产生漂移，`TestHermesExtensionIsTheProviderInstallWrites` 测试就会报错。

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。觉得有用？[去 GitHub 为 deja-vu 点颗星](https://github.com/vshulcz/deja-vu)。
