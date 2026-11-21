# deja-vu pour VS Code, Cursor, Windsurf & Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**Vos agents ne redéboguent plus ce que vous avez déjà corrigé.**

[deja-vu](https://github.com/vshulcz/deja-vu) indexe localement les historiques de session que Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code et 41 autres agents écrivent déjà sur disque — dans un seul binaire Go, sans LLM ni démon en arrière-plan.

**[Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Fonctionnalités

Affiche en direct l'état `deja statusline` dans la barre d'état de l'IDE (sessions indexées tous agents confondus, rappels servis aujourd'hui et historique de l'espace de travail). Si `deja` n'est pas encore installé, cliquer sur cet indicateur lance `npx -y @vshulcz/deja-vu install --auto` dans un terminal pour configurer tous les agents détectés.

## Installation rapide

```sh
brew install deja-vu && deja install --auto
# ou sans Homebrew :
npx -y @vshulcz/deja-vu install --auto
```

Installer l'extension de barre d'état depuis le CLI :

```sh
code --install-extension vshulcz.deja-status
```

## Impact mesuré

| Benchmark | Résultat |
|---|---|
| Précision sur LongMemEval-S (470 questions nettoyées) | **97.2% R@5**, **88.9% hit@1** |
| Coût en tokens sur des tâches répétées | **58% de tokens en moins**|
| Latence de requête lexicale en mémoire | Médiane de **0.7–0.8 ms** sur 52 agents de code |
| Indexation initiale sur 19,195 sessions réelles | **13.6 s**, sans clé API |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
