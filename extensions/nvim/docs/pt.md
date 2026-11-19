# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | Português | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte do **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 ms** no SQLite FTS5 · **~4%** de pegada do índice · **0** tokens de LLM na ingestão) · [Documentação](https://vshulcz.github.io/deja-vu/) · [Demo no navegador](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Busque transcrições anteriores de agentes de código, execute blame por linha de sessão de IA, recupere correções de erros e gere resumos de handoff entre Claude Code, Codex CLI, Cursor, opencode, Gemini CLI e mais 44 ferramentas direto do Neovim.

![Demonstração do deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Pré-requisitos

Instale o binário [`deja`](https://github.com/vshulcz/deja-vu) (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Instalação (`lazy.nvim`)

Como `lua/deja/` e `plugin/deja.lua` ficam na raiz do repositório, o `lazy.nvim` carrega `vshulcz/deja-vu` diretamente, sem pacotes extras:

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

Funciona prontamente com [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) ou o `vim.ui.select` nativo.

## Comandos e atalhos

| Comando | Atalho padrão (`keymaps = true`) | O que executa |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Busca textual completa em todas as sessões indexadas (`deja search --json`); aceita seleção visual |
| `:DejaLast [count]` | `<leader>dl` | Sessões recentes em todos os agentes instalados (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Qual sessão de IA escreveu ou editou a linha do cursor (`deja blame file:line --json`; `!` para o arquivo) |
| `:DejaHandoff [id]` | `<leader>dh` | Copia e abre o resumo de handoff entre agentes (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Recupera o que resolveu o diagnóstico LSP ou erro atual (`deja fix`) |
| `:DejaShow <id>` | — | Abre a transcrição com segredos redigidos em janela Markdown flutuante (`y` copia ID, `H` handoff, `R` retomar em split) |

## Barra de status (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` também entrega `lua/codecompanion/_extensions/deja/init.lua`, registrando o comando `/deja` (seletor interativo que anexa o handoff ao buffer de chat) e o contexto `#{deja}` / `#{deja:query}`:

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

Execute `:checkhealth deja` dentro do Neovim para verificar a detecção do binário e o picker em uso.

## Benchmarks

| Recall | Tokens | Indexação e busca |
|---|---|---|
| **97.2% R@5** no LongMemEval-S (88.9% hit@1 em 470 perguntas) | **58% menos tokens** (em tarefas repetidas) | **13.6 s** para indexar 19,195 sessões / **0.7 ms** de busca entre 49 agentes de código |

## Memória do repositório e handoff entre agentes

Compartilhe decisões validadas e caminhos descartados via git em `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). As entradas passam por redação de segredos ao gravar, ficam ordenadas por hash com `merge=union` para mesclar branches paralelos sem conflito e carregam para qualquer pessoa que clonar o repositório (`{"activations":{"auto":{"repo":false}}}` na política de confiança desativa a injeção via hooks). [Saiba mais](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Trocando de ferramenta no meio da tarefa? Execute `deja handoff --to <agent>` (ou chame `handoff` via MCP) para transferir objetivo, decisões, arquivos alterados, testes que passaram e trabalho em aberto para outro agente.

Parte do [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
