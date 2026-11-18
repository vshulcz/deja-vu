# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Recherche locale de sessions multi-agents, blame de ligne par IA et transfert de contexte pour IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm et Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) indexe localement dans SQLite FTS5 les transcriptions écrites sur disque par Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf et 39 autres agents de code, avec caviardage des secrets avant affichage et sans aucun service cloud.

**[★ Étoile sur GitHub](https://github.com/vshulcz/deja-vu)** · **[Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Fonctionnalités

- **Widget de barre d'état** : affiche en direct les statistiques `deja statusline` du projet actif à droite de la barre d'état. Un clic ouvre le menu d'actions `deja-vu`.
- **Rechercher des sessions IA (`Tools → deja-vu → Search AI Sessions...`)** : interroge les sessions locales des 49 agents pris en charge (`deja search --json`) et ouvre la transcription choisie dans un onglet Markdown en lecture seule.
- **Sessions récentes (`Tools → deja-vu → Recent AI Sessions`)** : liste les 50 dernières sessions de la machine (`deja last 50 --json`) avec filtrage instantané.
- **AI Blame sur la ligne courante (menu contextuel / `Tools → deja-vu → AI Blame Current Line`)** : exécute `deja blame <file>:<line> --json` (avec repli sur `<file>`) pour ouvrir la session IA qui a écrit ou modifié la ligne sous le curseur.
- **Copier le transfert multi-agents (`Tools → deja-vu → Copy Cross-Agent Handoff`)** : lance `deja handoff` à la racine du projet, copie le résumé dans le presse-papiers et l'affiche dans un onglet Markdown en lecture seule.

## Installation

1. Installez la CLI `deja` et connectez vos agents locaux :

```sh
brew install deja-vu && deja install --auto
# ou sans Homebrew :
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Installez le plugin JetBrains ([`com.github.vshulcz.deja-vu` sur JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)) :

- **Depuis JetBrains Marketplace** : [installer directement depuis plugins.jetbrains.com](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) ou ouvrir **Settings → Plugins → Marketplace** et chercher `deja-vu`.
- **Depuis le disque** : **Settings → Plugins → ⚙️ → Install Plugin from Disk...** avec `deja-jetbrains.zip` récupéré dans [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **En ligne de commande** :

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Remplacez `idea` par `goland`, `pycharm`, `webstorm`, `rustrover` ou `clion` selon votre IDE.)*

## Compiler depuis les sources

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

L'archive installable est générée dans `build/distributions/`.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions ; MemPalace publie 96.6%, agentmemory 95.2%) | **58% de tokens en moins** (**moitié moins de tokens** qu'agentmemory sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 49 agents de code (suivant sur 7 : 72 s) |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Utile ? [Mettez une étoile à deja-vu sur GitHub](https://github.com/vshulcz/deja-vu).
