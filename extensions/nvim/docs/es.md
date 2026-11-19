# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 ms** con SQLite FTS5 · **~4%** de tamaño de índice · **0** tokens LLM al indexar) · [Documentación](https://vshulcz.github.io/deja-vu/) · [Demo en navegador](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Busca transcripciones pasadas de agentes de código, ejecuta `blame` por línea, recupera soluciones a errores y genera resúmenes de relevo entre Claude Code, Codex CLI, Cursor, opencode, Gemini CLI y 44 agentes más directamente desde Neovim.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Requisitos previos

Instala el binario [`deja`](https://github.com/vshulcz/deja-vu) (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Instalación (`lazy.nvim`)

Como `lua/deja/` y `plugin/deja.lua` están en la raíz del repositorio, `lazy.nvim` carga `vshulcz/deja-vu` directamente sin paquetes intermedios:

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

Funciona de serie con [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) y el `vim.ui.select` nativo.

## Comandos y atajos

| Comando | Atajo por defecto (`keymaps = true`) | Qué ejecuta |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Búsqueda de texto completo en todas las sesiones indexadas (`deja search --json`); admite selección visual |
| `:DejaLast [count]` | `<leader>dl` | Sesiones recientes en todos los agentes instalados (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Qué sesión de IA escribió o editó la línea del cursor (`deja blame file:line --json`; `!` para el archivo completo) |
| `:DejaHandoff [id]` | `<leader>dh` | Copia y muestra un resumen de relevo entre agentes (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Recupera qué resolvió el diagnóstico LSP o error actual (`deja fix`) |
| `:DejaShow <id>` | — | Abre la transcripción redactada en una ventana flotante Markdown (`y` copiar ID, `H` relevo, `R` reanudar en división) |

## Barra de estado (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` incluye además `lua/codecompanion/_extensions/deja/init.lua`, que registra el comando `/deja` (selector interactivo que adjunta un relevo al chat) y el contexto `#{deja}` / `#{deja:query}`:

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

Ejecuta `:checkhealth deja` en Neovim para comprobar la detección del binario y el backend de selector activo.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 49 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
