# deja for Zed

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) 索引编程智能体本来就写在磁盘上的会话文件——Claude Code、Codex、Cursor、opencode 等其他四十九个智能体——并据此回答问题。这个扩展把那个索引作为 context server 接到 Zed 的 agent 面板上，于是一个对话可以检索你先前做过什么，包括装 deja 之前的那些工作。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 智能体可调用的工具

提供一个工具 `deja`，按 `mode` 参数切换能力：

| Mode | 回答内容 |
|---|---|
| `recall` | 匹配报错字符串、函数名、文件路径或 CLI 参数的历史会话。 |
| `context` | 当推理过程重要时，返回单次过往会话的完整摘要。 |
| `blame` | 在编辑或删除文件前，列出讨论过该文件的相关会话。 |
| `fix` | 这台机器上次遇到同类报错后执行的修复操作。 |
| `how` | 基于本机实际运行记录提取构建、测试或部署的真实命令。 |
| `orient` | 过往会话在此项目中运行过的命令与操作过的文件。 |
| `remember` | 写入一条可长期保留的工程决策，供后续召回。 |
| `handoff` | 读取另一会话的断点状态，从中断处继续工作。 |

## 安装

从 Zed 扩展列表中安装即可。首次使用时，扩展会让 Zed 执行 `deja --version`；若执行成功，就直接使用 Zed `PATH` 中的 `deja`。否则扩展会在自身目录下下载发布版二进制文件。由于 Zed 扩展在沙盒中运行、无法自行扫描常见安装目录，若 `deja` 不在启动 Zed 时的 `PATH` 中，请显式指定路径：

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` 同样支持接入 Zed，会直接将服务写入 `settings.json`——如果你已安装 CLI，这是最省事的方式。两者使用同一个服务 ID `deja-context-server`，而 Zed 按 ID 索引服务，因此无论先装哪边都只会保留一条配置：CLI 安装器看到扩展的配置会保持原样，后装的扩展也会命中同一个键而不是并存重复项。运行 `deja uninstall zed` 可移除 CLI 写入的配置。

索引与搜索在本地运行：未配置语义嵌入端点时不发起网络请求，并在建索引时对凭证和密钥自动脱敏。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 50 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

MIT 协议，与 deja 一致。

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
