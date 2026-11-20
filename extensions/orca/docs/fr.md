# deja-vu pour Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Plugin `trusted-worker` Orca v2 pour `deja` — apporte la mémoire de session sur 50 outils de développement IA (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca, etc.), y compris pour les sessions antérieures à l'installation de `deja`.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Prérequis : [`deja`](https://github.com/vshulcz/deja-vu#install) dans le `PATH` (ou sous `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin` ou `%LOCALAPPDATA%\Programs\deja`).

Installez le répertoire de plugin `extensions/orca` dans Orca (`>=1.4.248`).

Pour activer également MCP natif, les fichiers de skills, `SessionStart`, `PreCompact`, `SessionEnd` et la proposition de correctifs après une commande en erreur dans Orca, exécutez :

```sh
deja install orca-auto
```

(`deja install orca` installe MCP, skill et `/deja` sans hooks de cycle de vie ; `deja install orca-auto` ou `deja install --auto` installe tous les hooks.)

Lorsque `deja install orca-auto` a déjà inscrit le hook natif `UserPromptSubmit` dans `settings.json` d'Orca, ce worker se met automatiquement en retrait pour éviter la double injection `<deja-recall>`.

## Ce que ce plugin apporte

- **Hooks**
  - `agent.prompt.before` : résout l'espace de travail actif et injecte le contexte `<deja-recall>` pertinent dans le prompt dans la limite des 2 s allouées par Orca (délai `1.3s`, fail-open).
  - `agent.tool.before` : lors du lancement de sous-agents `Agent` ou `Task`, ajoute `<deja-recall>` uniquement à `input.prompt` (ne modifie jamais `Bash`, `Read`, `Edit`, etc.).
  - `agent.tool.after` : purement consultatif dans l'API hôte d'Orca (`next` est ignoré et Orca n'attend pas) ; l'injection de correctifs `fix` provient donc de `deja install orca-auto`.
- **Commandes**
  - `deja.orient` (`deja: Orient on workspace`) : lance `deja hook-context --plain` sur l'espace de travail actif et affiche un résumé.
  - `deja.recall` (`deja: Recall past sessions`) : exécute `deja search --limit 5 -- <query>` (ou `deja last -n 5` sans requête) et affiche le résultat.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 51 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

[MIT](../LICENSE)
