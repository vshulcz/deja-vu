# deja-vu Neovim 插件

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | 简体中文

属于 **[deja-vu](https://github.com/vshulcz/deja-vu)** 项目（SQLite FTS5 查询 **< 1 ms** · 索引体积约为原始会话的 **4%** · 入库 **0** LLM Token 开销）· [文档](https://vshulcz.github.io/deja-vu/) · [在线演示](https://vshulcz.github.io/deja-vu/view/) · [基准测试](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

在 Neovim 内全文检索 Claude Code、Codex CLI、Cursor、opencode、Gemini CLI 等全部四十三款编程智能体的本地会话记录，支持按光标所在行追溯改动来源（AI Blame）、回查报错修复过程以及生成跨智能体交接摘要。

## 前置条件

先在本地安装 [`deja-vu`](https://github.com/vshulcz/deja-vu) 命令行工具（需要 Neovim `>= 0.9`）：

```sh
brew install deja-vu && deja install --auto
```

## 安装（`lazy.nvim`）

仓库根目录直接提供标准 Neovim 运行时目录（`lua/deja/` 与 `plugin/deja.lua`），可直接通过 `lazy.nvim` 加载 `vshulcz/deja-vu`：

```lua
{
  "vshulcz/deja-vu",
  opts = {
    picker = "auto",        -- "auto" | "snacks" | "telescope" | "fzf" | "select"
    limit = 40,
    project_scoped = false,
    keymaps = true,         -- 绑定 <leader>dr, <leader>dl, <leader>db, <leader>dh, <leader>df
  },
}
```

自动适配 `snacks.nvim`、`telescope.nvim`（支持 `:Telescope deja`）、`fzf-lua` 或内置 `vim.ui.select`。

## 命令说明

- **`:DejaRecall [query]`**（`<leader>dr`）：全文检索本机所有智能体会话（`deja search --json`），可视模式下可直接搜索选中文本。
- **`:DejaLast [count]`**（`<leader>dl`）：浏览最近的跨智能体会话（`deja last --json`）。
- **`:DejaBlame[!]`**（`<leader>db`）：查询哪一次智能体会话编写或修改了当前光标所在行（`deja blame file:line --json`，加 `!` 查询整文件）。
- **`:DejaHandoff [id]`**（`<leader>dh`）：生成跨智能体交接摘要并复制到系统剪贴板（`+` 寄存器）。
- **`:DejaFix [error]`**（`<leader>df`）：自动读取当前行的 LSP 诊断信息，在历史会话中回查对应的修复方案（`deja fix`）。
- **`:DejaShow <id>`**：在浮动 Markdown 窗口中查看脱敏后的对话全文（按 `y` 复制会话 ID，按 `H` 生成交接摘要，按 `R` 在分屏终端恢复原会话）。

## CodeCompanion 集成（`codecompanion.nvim`）

内置 `codecompanion._extensions.deja` 扩展，为聊天缓冲区注册 `/deja` 斜杠命令与 `#{deja}` / `#{deja:query}` 上下文变量：

```lua
require("codecompanion").setup({
  extensions = { deja = { enabled = true } },
})
```

可在 Neovim 中执行 `:checkhealth deja` 检查可执行文件路径与选择器后端状态。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目，采用 MIT 许可。
