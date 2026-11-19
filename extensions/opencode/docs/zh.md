# opencode-deja

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

opencode 记得自己的会话。这个插件回答的是另一个问题：你在这台机器上 Claude Code、Codex、Cursor、Gemini、Zed 等其他四十八个编程智能体里做过什么，包括你安装任何东西之前的那几个月。基于单文件 Go 二进制 `deja` 与本地 BM25 索引，无大模型、无云端嵌入；可用 `DEJA_OFFLINE=1` 关闭每日版本检查。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

添加到你的 `opencode.ai/config.json`：

```json
{ "plugin": ["opencode-deja"] }
```

（opencode 2.x 使用复数键 `"plugins": ["opencode-deja"]`，也可直接运行 `opencode plugin add opencode-deja`。）或者运行 `deja install --auto`，将工具、规则与 `plugins/deja.js` 直接写入 `opencode.json`。

## 提供的能力

### 六个智能体按需工具

| 工具 | 作用 |
|---|---|
| `deja_recall` | 按错误信息、符号或主题检索过往会话历史 |
| `deja_session` | 读取某条命中会话附近的前后完整对话记录 |
| `deja_blame` | 查看某文件或 `path:line` 最初出自哪一次会话 |
| `deja_fix` | 查询以前解决同类报错时跑通的命令 |
| `deja_how` | 召回过去在这个项目里实际使用的命令行参数与构建标志 |
| `deja_remember` | 记录一条不再重新推导的已定结论 |

### 四项自动记忆注入

- **新会话启动提示**：opencode 1.x 会弹出简短提示条告知检索到的条数，opencode 2.x 会在后台直接写入系统提示；形如 `deja-vu — from Codex CLI, 4 months ago: "…"`。
- **按项目防抖冷却**：同一工作区约 20 分钟内不重复刷入同类提示；**自动跳过子智能体（sub-agent）**，只向主会话注入，节省上下文预算。
- **失败命令与关键文件决策**：命令执行后针对报错附带往期修法，或在修改过的文件旁带上历史结论。
- **压缩保留（Precompact）**：在上下文被压缩前整理当前任务目标、决定与未完成命令。

## 配置选项

在 1.x 配置里写成：

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

2.x 使用 `"plugins"` 键，选项说明如下：

- `autoRecall`：控制是否自动注入上下文，默认 `true`。
- `tools`：控制是否暴露六个 `deja_*` 工具，默认 `true`。
- `bin`：指定 `deja` 可执行文件的自定义路径。

如果需要给全机 49 款编程工具统一配置包含 `recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff` 在内的完整 MCP 与无缝切工具交接，请运行 `deja install --auto`（参见[切换智能体不丢上下文](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)）。

## 使用哪份二进制

查找顺序：`bin` 配置项 -> `DEJA_BIN` -> `PATH` 中的 `deja` -> `~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin` -> 随 npm 包附带的副本。

## 隐私

索引存放在 `~/.cache/deja/index.db`（可用 `DEJA_INDEX_DIR` 修改），在构建索引阶段自动清除 `sk-*`、`ghp_*`、`AKIA*` 与 `Bearer` 等凭据。默认完全离线，仅在主动使用 `deja embed` 并指定向量服务时才发生请求。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## ## 许可证

MIT
