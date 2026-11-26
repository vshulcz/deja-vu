# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（**< 1 ms** SQLite FTS5 検索 · インデックスサイズ **~4%** · 取込時 LLM トークン **0**）· [ドキュメント](https://vshulcz.github.io/deja-vu/) · [ブラウザデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Claude Code、Codex CLI、Cursor、opencode、Gemini CLI など計 53 のコーディングエージェントの過去ログ検索、行単位 AI セッション blame、エラー修正手順の想起、エージェント間引き継ぎダイジェスト生成を Neovim から直接実行できます。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 前提条件

[`deja`](https://github.com/vshulcz/deja-vu) バイナリをインストールします（`>= 0.24`、Neovim `>= 0.9`）：

```sh
brew install deja-vu && deja install --auto
```

## インストール（`lazy.nvim`）

`lua/deja/` と `plugin/deja.lua` がリポジトリ直下にあるため、`lazy.nvim` からラッパーなしで `vshulcz/deja-vu` を直接読み込めます：

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

[`snacks.nvim`](https://github.com/folke/snacks.nvim)、[`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim)（`:Telescope deja`）、[`fzf-lua`](https://github.com/ibhagwan/fzf-lua)、標準の `vim.ui.select` でそのまま動作します。

## コマンドとキーマップ

| コマンド | デフォルトキーマップ（`keymaps = true`） | 実行内容 |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | インデックス済み全セッションの全文検索（`deja search --json`）。ビジュアル選択対応 |
| `:DejaLast [count]` | `<leader>dl` | 導入済み全エージェントの直近セッション一覧（`deja last --json`） |
| `:DejaBlame[!]` | `<leader>db` | カーソル行を書いた・変更した AI セッションを特定（`deja blame file:line --json`、`!` でファイル全体） |
| `:DejaHandoff [id]` | `<leader>dh` | エージェント間引き継ぎダイジェストをコピーして表示（`deja handoff`） |
| `:DejaFix [error]` | `<leader>df` | 現在の LSP 診断やエラー文字列の解決履歴を取得（`deja fix`） |
| `:DejaShow <id>` | — | マスク済みトランスクリプトをフローティング Markdown ウィンドウで表示（`y`: ID コピー、`H`: 引き継ぎ、`R`: 分割再開） |

## ステータスライン（`lualine.nvim`）

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion（`codecompanion.nvim`）

`vshulcz/deja-vu` には `lua/codecompanion/_extensions/deja/init.lua` も同梱されており、`/deja` スラッシュコマンド（チャットバッファへ引き継ぎダイジェストを添付するピッカー）と `#{deja}` / `#{deja:query}` コンテキストを登録します：

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

Neovim 内で `:checkhealth deja` を実行すると、バイナリの検出状況と有効なピッカーを確認できます。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 53 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。
