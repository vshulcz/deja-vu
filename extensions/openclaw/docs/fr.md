# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Intègre l'historique sur disque de 48 autres agents de programmation (Claude Code, Codex, Cursor, Gemini, Zed, etc.) directement dans [OpenClaw](https://openclaw.ai). Fonctionne en local via le binaire Go unique `deja` et son index BM25, sans LLM (`DEJA_OFFLINE=1` désactive le contrôle quotidien).

## Installation

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Installation recommandée du binaire système :

```sh
brew install deja-vu
```

Si `deja install openclaw-auto` ou `deja install --auto` a déjà posé son pack, l'extension s'efface afin d'éviter toute injection en double.

## Fonctionnalités

- **`agent:bootstrap` (avec `openclaw agent --local`)** : injecte dès l'ouverture les décisions établies dans le projet.
- **`before_prompt_build` (avec OpenClaw `2026.8.1` et `hooks.allowConversationAccess: true`)** : ajoute les extraits pertinents avant chaque réponse.
- **`before_compaction`** : sauvegarde tâche en cours, conclusions et commandes avant la compaction.
- **`session_end`** : actualise l'index local en fin de session.
- **Middleware d'outils** : complète les échecs de commande et modifications de fichiers avec les solutions ou décisions antérieures.
- **Outils `deja_recall` / `deja_fix` / `deja_blame`** : recherche, correctifs de commandes et provenance d'un fichier.
- **`/deja <query>`** : commande rapide couvrant l'ensemble des historiques.

### Mention de provenance en message direct

Sur les réponses en message privé admissibles, `reply_payload_sending` ajoute une ligne en italique :

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

Cette ligne n'apparaît que lorsque ces quatre conditions sont réunies :

1. Session directe hors groupe (`isGroup: false` / `direct`).
2. Expéditeur conforme à `commands.ownerAllowFrom` / `allowFrom`.
3. Streaming par blocs désactivé (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. Réponse textuelle finale envoyée sur le canal `direct`.

Ajoutez `"receipt": false` pour masquer cette mention.

## Configuration

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

Ou via la CLI :

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Résolution du binaire

`bin` -> `DEJA_BIN` -> `PATH` -> chemins usuels -> copie incluse dans `@vshulcz/deja-vu`.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 49 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Confidentialité

Index stocké dans `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`) avec masquage des clés `sk-*`, `ghp_*`, `AKIA*` et `Bearer`. Tout demeure local sans `deja embed`. Configurez les 49 agents pris en charge d'une seule commande avec `deja install --auto`.

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
