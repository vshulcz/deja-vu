# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 ms** SQLite FTS5-Suche · **~4%** Index-Overhead · **0** LLM-Tokens beim Indizieren) · [Dokumentation](https://vshulcz.github.io/deja-vu/) · [Browser-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Durchsuche frühere Coding-Agent-Protokolle, starte zeilenweises AI-Session-Blame, finde Fehlerlösungen wieder und erstelle Handoff-Zusammenfassungen über Claude Code, Codex CLI, Cursor, opencode, Gemini CLI und 44 weitere Tools direkt aus Neovim.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Voraussetzungen

Installiere das [`deja`](https://github.com/vshulcz/deja-vu)-Binary (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Installation (`lazy.nvim`)

Da `lua/deja/` und `plugin/deja.lua` direkt im Repository-Stamm liegen, lädt `lazy.nvim` `vshulcz/deja-vu` unmittelbar ohne Wrapper-Paket:

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

Funktioniert direkt mit [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) oder dem nativen `vim.ui.select`.

## Befehle & Tastenkürzel

| Befehl | Standardkürzel (`keymaps = true`) | Ausgeführte Aktion |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Volltextsuche über alle indizierten Sessions (`deja search --json`); unterstützt visuelle Auswahl |
| `:DejaLast [count]` | `<leader>dl` | Letzte Sessions über alle installierten Agents (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Welche KI-Session die Cursor-Zeile geschrieben oder bearbeitet hat (`deja blame file:line --json`; `!` für ganze Datei) |
| `:DejaHandoff [id]` | `<leader>dh` | Handoff-Zusammenfassung kopieren und anzeigen (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Frühere Lösung für die aktuelle LSP-Meldung oder Fehlermeldung abrufen (`deja fix`) |
| `:DejaShow <id>` | — | Bereinigtes Protokoll in einem schwebenden Markdown-Fenster öffnen (`y` ID kopieren, `H` Handoff, `R` im Split fortsetzen) |

## Statuszeile (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` enthält zudem `lua/codecompanion/_extensions/deja/init.lua` und registriert den Slash-Befehl `/deja` (interaktiver Session-Picker, der ein Handoff an den Chat anhängt) sowie `#{deja}` / `#{deja:query}` als Editor-Kontext:

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

Führe `:checkhealth deja` in Neovim aus, um Binary-Pfad und aktives Picker-Backend zu prüfen.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 49 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
