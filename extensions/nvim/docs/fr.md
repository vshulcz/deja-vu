# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**< 1 ms** SQLite FTS5 · **~4%** d'empreinte d'index · **0** token LLM à l'ingestion) · [Documentation](https://vshulcz.github.io/deja-vu/) · [Démo navigateur](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Recherchez vos anciennes transcriptions d'agents, lancez un `blame` de ligne par session IA, retrouvez des correctifs d'erreurs et générez des transferts inter-agents couvrant Claude Code, Codex CLI, Cursor, opencode, Gemini CLI et 44 autres outils directement depuis Neovim.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Prérequis

Installez le binaire [`deja`](https://github.com/vshulcz/deja-vu) (`>= 0.24`, Neovim `>= 0.9`) :

```sh
brew install deja-vu && deja install --auto
```

## Installation (`lazy.nvim`)

Comme `lua/deja/` et `plugin/deja.lua` résident à la racine du dépôt, `lazy.nvim` charge `vshulcz/deja-vu` directement sans paquet enveloppe :

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

Fonctionne d'emblée avec [`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) ou `vim.ui.select` natif.

## Commandes & Raccourcis

| Commande | Raccourci par défaut (`keymaps = true`) | Action |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | Recherche plein texte dans toutes les sessions indexées (`deja search --json`) ; sélection visuelle prise en charge |
| `:DejaLast [count]` | `<leader>dl` | Sessions récentes de tous les agents installés (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | Quelle session IA a écrit ou modifié la ligne du curseur (`deja blame file:line --json` ; `!` pour tout le fichier) |
| `:DejaHandoff [id]` | `<leader>dh` | Copie et affiche un résumé de transfert multi-agents (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Retrouve ce qui a résolu le diagnostic LSP ou l'erreur courante (`deja fix`) |
| `:DejaShow <id>` | — | Ouvre la transcription caviardée dans une fenêtre Markdown flottante (`y` copier l'ID, `H` transfert, `R` reprendre en split) |

## Barre d'état (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` fournit aussi `lua/codecompanion/_extensions/deja/init.lua`, enregistrant la commande `/deja` (sélecteur de session qui joint un transfert au tampon de discussion) et le contexte éditeur `#{deja}` / `#{deja:query}` :

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

Exécutez `:checkhealth deja` dans Neovim pour vérifier la détection du binaire et le sélecteur actif.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 49 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
