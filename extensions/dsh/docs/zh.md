# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 扩展（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 个会话 · token 用量为 agentmemory 的**一半**）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

面向 DeepSeek Harness（`dsh`）与 DSH Desktop（`anywhere-labs/dsh-desktop`）的 `session-query` 插件。除了 DSH 自身的对话历史，deja 还索引 Claude Code、Codex、Cursor、opencode、Antigravity、Grok Build、Kimi、Cline、Zed 等其他四十八个智能体本来就写在磁盘上的会话文件。不需要提前录制：历史已经在那里，包括安装 deja 之前的那些月份。如果你上周才转到 dsh，上周之前做过的事仍然可以在 dsh 里查到（全部由本地单文件 Go 二进制 `deja` 与 BM25 索引完成，无 LLM，可用 `DEJA_OFFLINE=1` 关闭每日检查）。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安装

通过 npm 或 GitHub 源码直接安装到 `web` profile（必须携带 `--profile`）：

```sh
dsh plugin --profile web add dsh-deja
# 或直接从仓库路径安装开发版：
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

若系统未预先安装 `deja`，请配置 `DEJA_BIN`，或使用安装脚本 / Homebrew 一并完成全机 49 款智能体与 `$DSH_HOME/plugins/deja/` 的配置：

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## 插件内容

### 六个智能体工具

| 工具 | 作用 |
|---|---|
| `deja_recall` | 按错误信息、主题或标志词搜索跨智能体会话历史 |
| `deja_session` | 拉取命中片段前后的完整对话轮次 |
| `deja_blame` | 查找修改过指定文件或 `path:line` 的历史会话 |
| `deja_fix` | 查找上次修通同类错误所使用的命令 |
| `deja_how` | 查看此项目里实际运行过的构建与测试参数 |
| `deja_remember` | 记录不再反复推导的项目约定 |

- **`/deja <...>` 命令**：在聊天输入框中直接带过滤条件检索历史。
- **独立挂载与界面**：运行 `deja install dsh-auto` 或安装后，`dsh web`（以及 `tui`、`headless`、`sdk` 模式）会加载侧边栏与查询接口；若要关闭自动注入可在 YAML 配置设 `autoRecall: false`。

## 搜索快捷键与面板控制

在 `Settings → Deja` 中可查看或自定义全部交互控制：

- **Mod+F**（或 **Mod+Alt+F** / macOS 上的 **Cmd+Option+F**）：打开当前对话工作区的检索框；生成注入指令时可写 `/deja --session <bare session UUID> -- <your draft>` 将范围限定到单次会话。
- **Mod+Shift+K**（macOS **Cmd+Shift+K**）：打开全工作区跨智能体快速检索。
- **Mod+K**：唤醒面板并在开启 **DSH conversations only** 开关后只查 DSH 会话（单次最多返回 20 条命中）。
- **Linux 浏览器快捷键冲突规避**：部分 Linux 桌面浏览器占用上述组合时，可使用备用快捷键 **`Mod+/`**、**`Mod+Shift+,`**、**`Mod+Shift+.`**（对应配置键 `shortcuts.open`）。

## 自定义快捷键

在 `Settings → Deja` 里点击 **`session.search`** 旁的 **Change shortcut** 即可录制新按键，**Restore default** 恢复默认值，**Remove** 解绑该快捷键。底层面板由 **dsh-deja package host plugin** 提供路由 `/api/deja-search`；请求 `/api/deja-search?probe=1` 返回 `{"ok": true, "plugin": "dsh-deja"}` 即表示与本地 `deja search` 后端打通。

## 实现接入细节

- **对话上下文钩子**：通过 `ctx.systemPrompt.context` 与 `agent/pre-step`（支持 `dsh 0.1.1-rc.2` 及以上）写入 `additionalContext` / `systemMessage`。
- **HTTP 与 CLI 接口**：插件暴露 `/api/deja-notes`（底层调用 `--plain`）供 `client.js` 读取；`GET /api/deja-search` 转调 `deja search --harness deepseek --json --limit 20 -- <query>`，设置 20 秒超时、2 MiB 输出上限与 500 字符查询上限。
- **侧边栏挂载槽位**：基于 `data-slot="sidebar.workspaces"`、`aria-expanded` 与 `_listArea` 实现可折叠侧边栏卡片（针对 DSH `0.2.1-rc.5` 优化兼容）。

## 测试搜索控件

```sh
node --test extensions/dsh/test/*.test.js
```

无构建环境下的浏览器交互测试页面位于 `extensions/dsh/test/search-browser.html`（通过 React 18 CDN 直接打开运行）。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 为 95.2%） | **token 减少 58%**（同一仓储重复任务上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms**（7 款对比工具中第二名 72 秒） |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

## 不会做什么

索引严格留在 `~/.cache/deja/index.db`（可通过 `DEJA_INDEX_DIR` 移动），除非你显式运行 `deja embed`，否则不会发送任何数据给外部嵌入模型或云服务。`sk-*`、`ghp_*`、`AKIA*` 与 `Bearer` 密钥会在建立索引时自动清除，且从不改写原始会话文件。

## 许可证

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。觉得有用？[去 GitHub 为 deja-vu 点颗星](https://github.com/vshulcz/deja-vu)。
