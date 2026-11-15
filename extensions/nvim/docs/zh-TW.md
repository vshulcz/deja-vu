# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 生態組件（**< 1 ms** SQLite FTS5 查詢 · **~4%** 索引體積佔用 · 索引階段 **0** LLM token）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

直接在 Neovim 內檢索 Claude Code、Codex CLI、Cursor、opencode、Gemini CLI 與其他 43 款程式設計工具的歷史會話紀錄、執行行級 AI 會話溯源、找回錯誤修復方式，並產生跨代理接力摘要。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 事前需求

請先安裝 [`deja`](https://github.com/vshulcz/deja-vu) 執行檔（`>= 0.24`，Neovim `>= 0.9`）：

```sh
brew install deja-vu && deja install --auto
```

## 安裝（`lazy.nvim`）

由於 `lua/deja/` 與 `plugin/deja.lua` 位於儲存庫根目錄，`lazy.nvim` 可直接載入 `vshulcz/deja-vu`，無須額外包裝套件：

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

開箱支援 [`snacks.nvim`](https://github.com/folke/snacks.nvim)、[`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim)（`:Telescope deja`）、[`fzf-lua`](https://github.com/ibhagwan/fzf-lua) 與原生 `vim.ui.select`。

## 指令與快速鍵

| 指令 | 預設快速鍵（`keymaps = true`） | 對應行為 |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | 在所有已索引會話中全文搜尋（`deja search --json`）；支援選取文字 |
| `:DejaLast [count]` | `<leader>dl` | 列出本機所有已安裝代理的近期會話（`deja last --json`） |
| `:DejaBlame[!]` | `<leader>db` | 查詢游標所在行由哪次 AI 會話撰寫或編輯（`deja blame file:line --json`；加 `!` 查整份檔案） |
| `:DejaHandoff [id]` | `<leader>dh` | 複製並檢視跨代理接力摘要（`deja handoff`） |
| `:DejaFix [error]` | `<leader>df` | 依據目前的 LSP 診斷或錯誤訊息召回歷史解法（`deja fix`） |
| `:DejaShow <id>` | — | 在浮動 Markdown 視窗開啟脫敏後的會話紀錄（`y` 複製 ID、`H` 接力、`R` 分割視窗恢復） |

## 狀態列（`lualine.nvim`）

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion（`codecompanion.nvim`）

`vshulcz/deja-vu` 亦內建 `lua/codecompanion/_extensions/deja/init.lua`，註冊 `/deja` 斜線指令（互動選擇會話並附加接力摘要至對話緩衝區）以及 `#{deja}` / `#{deja:query}` 編輯器上下文：

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

在 Neovim 執行 `:checkhealth deja`，可確認執行檔偵測狀態與作用中的選擇器後端。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 為 95.2%） | **token 減少 58%**（同儲存庫重複任務上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 48 個程式設計代理的檢索僅 **0.7 ms**（7 款對比工具中第二名 72 秒） |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。覺得實用？[到 GitHub 為 deja-vu 點顆星](https://github.com/vshulcz/deja-vu)。
