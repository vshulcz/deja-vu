# deja-vu for Spec Kit

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

一个 [Spec Kit](https://github.com/github/spec-kit) 扩展。在实现某个功能之前，`/speckit.deja-vu.recall` 会搜索你过往的编程智能体会话记录，汇总此前尝试过的方案、已确定的决策以及失败的路线，并在每条结论后附上对应的会话 ID。

会话数据来自 [deja](https://github.com/vshulcz/deja-vu)，它直接索引 Claude Code、Codex、Cursor、Copilot、Gemini 等智能体已经写入磁盘的记录。所有数据全部保留在本机。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

先安装 `deja`：

```sh
brew install deja-vu
# 或
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

随后在 Spec Kit 项目中添加扩展：

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

每个 deja 版本都会附带同版本的 `deja-spec-kit.zip`。若想试用本地源码目录，可运行：`specify extension add --dev /path/to/deja-vu/extensions/spec-kit`。

## 用法

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

不传参数时，扩展会读取当前功能的 `spec.md` 与 `plan.md` 并自动提取检索词；传入参数时则按指定关键词搜索。命令前缀取决于所用智能体的格式，可能会显示为 `/speckit-deja-vu-recall` 或 `$speckit-deja-vu-recall`。

它在底层会在仓库根目录执行以下命令：

```sh
deja recall -- "<query>"   # 匹配历史会话（当前项目优先）
deja files -- "<query>"    # 与该主题相关的读写文件
```

当摘要过短不足以判断上下文时，会继续调用 `deja show <id>`。最终生成一份 `Prior sessions` 报告，拆分为已尝试与失败方案、已定决策、相关文件与待办事项，每条均标注 `(deja:<id>)`。

## 钩子（Hook）

扩展注册了一个可选的 `before_implement` 钩子。运行 `/speckit.implement` 时，智能体会先询问是否运行历史召回；未经确认不会自动执行。如需关闭，可在 `.specify/extensions.yml` 中设置 `enabled: false`，或执行 `specify extension disable deja-vu`。

## 说明

- 只读操作：该命令仅检索与读取，不会修改任何文件。
- 首次在本机调用 deja 会构建索引，若历史记录较大可能需要片刻；此后每次查询均在毫秒级返回。
- 召回的历史文本仅作参考数据；命令提示词明确指示智能体绝不执行历史文本中的任何指令。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 许可证

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
