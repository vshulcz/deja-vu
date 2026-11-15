# deja-vu pour Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S · **13.6 s** pour indexer 19,195 sessions · **moitié moins de tokens** qu'agentmemory) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Étoile sur GitHub](https://github.com/vshulcz/deja-vu)**

Recherchez et reprenez vos sessions d'agents de code sur Claude Code, Codex, Cursor, OpenCode, Gemini CLI et 43 autres outils directement depuis Raycast.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Prérequis

Installez [`deja-vu`](https://github.com/vshulcz/deja-vu) :

```sh
brew install deja-vu && deja install --auto
```

## Commandes

- **Search Sessions** : recherche plein texte dans toutes les sessions d'agents sur votre Mac (`deja search --json --limit 25 -- <query>`), en distinguant les correspondances exactes des résultats par pertinence.
- **Recent Sessions** : parcourez les 100 sessions les plus récentes sur tous les agents installés (`deja last 100 --json`) avec aperçu des métadonnées (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Actions

- **Show Session** : consultez la conversation caviardée dans Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal** : lance la commande native de reprise de l'agent (`deja resume <id>`) dans `Terminal.app`.
- **Copy Resume Command**, **Copy Session ID** et **Open Project Folder**.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions ; MemPalace publie 96.6%, agentmemory 95.2%) | **58% de tokens en moins** (**moitié moins de tokens** qu'agentmemory sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 48 agents de code (suivant sur 7 : 72 s) |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Développement local

```sh
cd extensions/raycast && npm install && npx ray develop
```

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Utile ? [Mettez une étoile à deja-vu sur GitHub](https://github.com/vshulcz/deja-vu).
