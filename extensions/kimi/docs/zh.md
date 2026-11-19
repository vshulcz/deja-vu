# deja for Kimi Code

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) 索引编程智能体本来就写在磁盘上的会话文件——Claude Code、Codex、Cursor、opencode 等其他四十八个智能体——并据此回答问题。这个插件把那个索引带进 Kimi Code：召回随提问一起到达，智能体也可以自己检索历史。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

该写法会固定最新发行版并记录来源地址，方便 Kimi 检查更新。Release 页面也提供了独立包 `kimi-deja.zip`（约 10 KB，无需拉取整个仓库），适用于市场条目或轻量安装。

若本机尚未安装 `deja` 二进制文件，请先安装（并可选一次性接入本机其他智能体）：

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

插件从受管副本加载，安装后请运行 `/reload` 或 `/new`。

## 更新

Kimi 仅对从其官方市场安装的插件推送更新提醒。从仓库安装时，再次执行 `/plugins install` 即可拉取当前最新发行版；若本地副本落后于当前 `deja` 内置版本，`deja doctor` 会给出提示：

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## 包含功能

- **与 `deja install kimi-auto` 同款的钩子**。在会话首条提问时注入本项目的既定结论（`deja hook-context --once`）；每次提问按问题相关性按需检索（`deja hook-prompt`），Kimi 将结果追加到本轮请求中，无匹配则静默；在压缩之前读取即将折叠的轮次，使下一条提示仍保留中断前的工作上下文（`deja hook-precompact`）；在 `SessionEnd` 时移除当前会话的活跃标记（`deja hook-session-end`）。
- **工具**。插件将 `deja mcp` 注册为 MCP 服务：提供统一工具 `deja`，支持 `recall`、`context`、`blame`、`fix`、`how`、`orient`、`remember`、`handoff` 模式。
- **`/deja:recall <query>`**：用于手动直接查询历史。
- **`deja-history` 技能**：在会话开始时加载，引导智能体在重复排查前先查阅历史记录。

## 二进制选择顺序

按顺序查找：`DEJA_BIN` 环境变量、你自行安装的 `deja`（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin`），最后才是 `PATH` 中的 `deja`。你自己执行的 `deja update` 或 `brew upgrade deja-vu` 始终优先于插件发行版锁定的版本。

## 如果你也运行过 `deja install kimi`

CLI 会在 `~/.kimi-code/mcp.json` 中写入同一个 MCP 服务并在 `config.toml` 中写入相同的钩子，而 Kimi 会将插件服务运行在自有命名空间下（`plugin-deja:deja`），否则两者会同时生效——每个工具出现两次、每次提问召回两次。

本插件在钩子触发和服务端启动时都会实时检测安装器的配置；一旦发现 CLI 安装的配置即主动让位，以 `deja install` 持续维护的那份为准。执行 `deja uninstall kimi` 后无需重装插件即可自动恢复插件接管。

当接入仅由插件提供时，`deja doctor` 会对 kimi 报告 `plugin`，避免本机被误报为未配置。

有两种情况插件无法自动感知：你手动添加的、名称不是 `deja` 的 MCP 条目（它会与插件并存运行），以及你自己编写的、不带安装器标记注释而直接调用 deja 的钩子。

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
