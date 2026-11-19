# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | 简体中文 | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 生态组件（**< 1 ms** SQLite FTS5 查询 · **~4%** 索引空间占用 · 索引阶段 **0** LLM token）· [文档](https://vshulcz.github.io/deja-vu/zh/) · [浏览器演示](https://vshulcz.github.io/deja-vu/view/) · [评测](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

在 Neovim 中直接检索 Claude Code、Codex CLI、Cursor、opencode、Gemini CLI 以及其他 44 款编程工具的历史会话记录，运行代码行级 AI 会话溯源，召回报错修复方案，并生成跨智能体接力摘要。

![deja-vu 演示](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 前置要求

先安装 [`deja`](https://github.com/vshulcz/deja-vu) 可执行文件（`>= 0.24`，Neovim `>= 0.9`）：

```sh
brew install deja-vu && deja install --auto
```

## 安装（`lazy.nvim`）

由于 `lua/deja/` 与 `plugin/deja.lua` 位于仓库根目录，`lazy.nvim` 可以直接加载 `vshulcz/deja-vu`，无需额外包装插件：

```lua
{
  "vshulcz/deja-vu",
  opts = {
    picker = "auto",        -- "auto" | "snacks" | "telescope" | "fzf" | "select"
    limit = 40,
    project_scoped = false,
    keymaps = true,         -- <leader>dr, <leader>dl, <leader>db, <leader>dh, <leader>df
  },
}
```

开箱适配 [`snacks.nvim`](https://github.com/folke/snacks.nvim)、[`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim)（`:Telescope deja`）、[`fzf-lua`](https://github.com/ibhagwan/fzf-lua) 与原生 `vim.ui.select`。

## 命令与快捷键

| 命令 | 默认快捷键（`keymaps = true`） | 执行内容 |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | 在全部已索引会话中全文搜索（`deja search --json`）；支持可视选区 |
| `:DejaLast [count]` | `<leader>dl` | 列出本机所有已安装智能体的近期会话（`deja last --json`） |
| `:DejaBlame[!]` | `<leader>db` | 查看光标行由哪次 AI 编程会话写入或修改（`deja blame file:line --json`；加 `!` 查整文件） |
| `:DejaHandoff [id]` | `<leader>dh` | 复制并查看跨智能体接力摘要（`deja handoff`） |
| `:DejaFix [error]` | `<leader>df` | 根据当前 LSP 诊断或报错文本召回历史修复方案（`deja fix`） |
| `:DejaShow <id>` | — | 在浮动 Markdown 窗口中打开脱敏后的会话记录（`y` 复制 ID、`H` 接力、`R` 分屏恢复） |

## 状态栏（`lualine.nvim`）

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion（`codecompanion.nvim`）

`vshulcz/deja-vu` 同时内置 `lua/codecompanion/_extensions/deja/init.lua`，注册 `/deja` 斜杠命令（通过交互式选择器将会话接力摘要插入聊天缓冲区）以及 `#{deja}` / `#{deja:query}` 编辑器上下文变量：

```lua
{
  "olimorris/codecompanion.nvim",
  dependencies = { "vshulcz/deja-vu" },
  opts = {
    extensions = {
      deja = { enabled = true },
    },
  },
}
```

在 Neovim 中运行 `:checkhealth deja`，即可验证二进制发现状态与当前生效的选择器后端。

## 基准评测

| 召回率 | Token 用量 | 索引与检索耗时 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 题清洗后测得 88.9% hit@1） | **token 减少 58%**（同一仓储重复任务） | **13.6 秒**索引 19,195 个历史会话 / 跨越 49 个编程智能体的检索仅 **0.7 ms** |

## 仓库记忆与跨智能体接力

通过 git 在 `.deja/memory.jsonl` 中共享经过筛选的项目决策与踩坑记录（`deja repo candidates`、`deja repo add <id>`）。条目在写入前自动脱敏，按内容哈希排序并附带 `merge=union`，并发分支可干净合并；克隆仓库的协作者都会自动加载（在信任策略中配置 `{"activations":{"auto":{"repo":false}}}` 可关闭 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/zh/guide/repo-memory.html)。

开发到一半需要切换工具？运行 `deja handoff --to <agent>`（或通过 MCP 调用 `handoff`），即可将当前会话的目标、已定决策、修改过的文件、通过的检查与待办工作直接交给另一个编程智能体。

[deja-vu](https://github.com/vshulcz/deja-vu) 生态组件。MIT 协议。
