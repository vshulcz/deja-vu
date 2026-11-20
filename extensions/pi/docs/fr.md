# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Mémoire locale pour [pi](https://github.com/badlogic/pi-mono) s'appuyant sur les historiques de sessions déjà enregistrés sur disque par 48 autres agents de code (Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes, etc.). Tout repose sur l'exécutable Go unique `deja` et son index BM25 local, sans LLM ni embeddings ; le réseau n'est utilisé que lors des commandes explicites et de la vérification quotidienne désactivable via `DEJA_OFFLINE=1`.

## Installation

```sh
pi install npm:@vshulcz/pi-deja
```

Si `deja` n'est pas encore installé globalement, le greffon se rabat sur la copie embarquée selon l'ordre défini dans [Quel binaire est utilisé](#quel-binaire-est-utilisé). Vous pouvez aussi installer le binaire principal en amont :

```sh
brew install deja-vu
```

Si `deja install pi-auto` ou `deja install --auto` a déjà posé l'extension autonome, ce paquet npm s'efface automatiquement pour éviter tout doublon d'outils ou d'injection. Le même mécanisme s'applique à `omp` via `pi.extensions` et `before_agent_start`.

## Fonctionnalités

- **Début de session (barre d'état en pied de page)** : affiche les sessions indexées et les correspondances pour le dépôt courant.
- **À chaque prompt (`before_agent_start`)** : recherche les choix d'architecture et correctifs passés du projet et les injecte au modèle.
- **Après une commande en échec (`tool_result` sur `bash` / `powershell`)** : rappelle quelle commande avait corrigé la même erreur auparavant.
- **À la lecture/édition (`tool_result` read/edit/write)** : signale les décisions antérieures concernant les fichiers manipulés.
- **Après compaction (`session_compact`)** : réinjecte la tâche en cours, les conclusions et les commandes ouvertes après le résumé.
- **En fin de session (`session_shutdown`)** : met à jour l'index local avec la session terminée.
- **`/deja <query>`** : commande slash pour interroger l'historique multi-agents à la demande.

## Quel binaire est utilisé

L'ordre de résolution du binaire `deja` est le suivant :

1. Variable d'environnement `DEJA_BIN`
2. `deja` présent dans `PATH`
3. Emplacements d'installation usuels (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, etc.)
4. Binaire fourni par `@vshulcz/deja-vu`

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 51 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Confidentialité

L'index est stocké dans `~/.cache/deja/index.db` (configurable via `DEJA_INDEX_DIR`) avec masquage automatique des secrets `sk-*`, `ghp_*`, `AKIA*` et `Bearer`. Tout reste local, sauf si vous configurez volontairement `deja embed`. Déployez `recall`, `fix`, `blame` et `handoff` sur les 51 agents pris en charge via `deja install --auto`.

## Licence

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
