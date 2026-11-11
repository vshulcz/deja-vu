# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/nvim/docs/zh.md)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 ms** SQLite FTS5 lookup · **~4%** index footprint · **0** LLM tokens at ingest) · [Documentation](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Search past coding agent transcripts, run line-level AI session blame, recall error fixes, and generate cross-agent handoff digests across Claude Code, Codex CLI, Cursor, opencode, Gemini CLI, and thirty-eight more harnesses directly from Neovim.

## Prerequisites

Install the [`deja`](https://github.com/vshulcz/deja-vu) binary (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Installation (`lazy.nvim`)

Because `lua/deja/` and `plugin/deja.lua` sit at the repository root, `lazy.nvim` loads `vshulcz/deja-vu` directly without wrapper packages:

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

Works out of the box with [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua), or native `vim.ui.select`.

## Commands & Keymaps

| Command | Default keymap (`keymaps = true`) | What it runs |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Full-text search across every indexed session (`deja search --json`); visual selection supported |
| `:DejaLast [count]` | `<leader>dl` | Recent sessions across all installed agents (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Which AI coding session wrote or edited the cursor line (`deja blame file:line --json`; `!` for file) |
| `:DejaHandoff [id]` | `<leader>dh` | Copy and view a cross-agent handoff digest (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Recall what resolved the current LSP diagnostic or error string (`deja fix`) |
| `:DejaShow <id>` | — | Open the redacted session transcript in a floating Markdown window (`y` yank ID, `H` handoff, `R` resume in split) |

## Statusline (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` also ships `lua/codecompanion/_extensions/deja/init.lua`, registering the `/deja` slash command (interactive session picker that attaches a handoff digest to the chat buffer) and `#{deja}` / `#{deja:query}` editor context:

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

Run `:checkhealth deja` inside Neovim to verify binary discovery and active picker backend.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
