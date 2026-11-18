# deja-vu для Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 мс** поиск в SQLite FTS5 · **~4%** размер индекса · **0** LLM-токенов на индексацию) · [Документация](https://vshulcz.github.io/deja-vu/) · [Демо в браузере](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Ищите по прошлым сессиям кодинг-агентов, запускайте построчный AI-blame, находите решения старых ошибок и формируйте сводки передачи контекста для Claude Code, Codex CLI, Cursor, opencode, Gemini CLI и ещё 44 инструментов прямо из Neovim.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Предварительные требования

Установите бинарник [`deja`](https://github.com/vshulcz/deja-vu) (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Установка (`lazy.nvim`)

Поскольку `lua/deja/` и `plugin/deja.lua` находятся в корне репозитория, `lazy.nvim` загружает `vshulcz/deja-vu` напрямую без обёрточных пакетов:

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

Работает из коробки с [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) или встроенным `vim.ui.select`.

## Команды и горячие клавиши

| Команда | Сочетание по умолчанию (`keymaps = true`) | Что запускает |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Полнотекстовый поиск по всем проиндексированным сессиям (`deja search --json`); поддерживает визуальное выделение |
| `:DejaLast [count]` | `<leader>dl` | Недавние сессии всех установленных агентов (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Какая AI-сессия написала или изменила строку под курсором (`deja blame file:line --json`; `!` для всего файла) |
| `:DejaHandoff [id]` | `<leader>dh` | Скопировать и открыть межагентную сводку передачи (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Вспомнить, что помогло при текущей диагностике LSP или ошибке (`deja fix`) |
| `:DejaShow <id>` | — | Открыть очищенный от секретов лог сессии во всплывающем окне Markdown (`y` — копировать ID, `H` — передача, `R` — открыть в сплите) |

## Строка состояния (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` также включает `lua/codecompanion/_extensions/deja/init.lua`, добавляя слэш-команду `/deja` (интерактивный выбор сессии с прикреплением handoff-сводки в буфер чата) и контекст `#{deja}` / `#{deja:query}`:

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

Выполните `:checkhealth deja` в Neovim, чтобы проверить поиск бинарника и активный пикер.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах; у MemPalace заявлено 96.6%, у agentmemory — 95.2%) | **на 58% меньше токенов** (**вдвое меньше токенов**, чем у agentmemory на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 49 кодинг-агентам (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT. Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
