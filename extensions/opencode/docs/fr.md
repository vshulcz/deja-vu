# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Mémoire locale pour [opencode](https://opencode.ai) nourrie par l'historique des sessions de 48 autres agents de code sur votre machine (Claude Code, Codex, Cursor, Gemini, Hermes, Zed, etc.). Propulsée par le binaire Go unique `deja` et son index BM25 local, sans LLM (`DEJA_OFFLINE=1` coupe le contrôle quotidien).

## Installation

Dans `opencode.ai/config.json` :

```json
{ "plugin": ["opencode-deja"] }
```

(Avec opencode 2.x, utilisez `"plugins": ["opencode-deja"]` ou `opencode plugin add opencode-deja`.) Sinon, `deja install --auto` configure directement `opencode.json` et `plugins/deja.js`.

## Ce qui est fourni

### Six outils à la demande

| Outil | Rôle |
|---|---|
| `deja_recall` | Rechercher dans l'historique multi-agents |
| `deja_session` | Lire les échanges complets autour d'une correspondance |
| `deja_blame` | Identifier la session à l'origine d'un fichier ou `path:line` |
| `deja_fix` | Retrouver la commande qui a résolu une erreur identique |
| `deja_how` | Reconstituer les options exactes utilisées dans ce projet |
| `deja_remember` | Enregistrer une décision stabilisée |

### Quatre mécanismes automatiques

- **Rappel au démarrage** : toast discret sous opencode 1.x ou contexte système sous 2.x (`deja-vu — from Codex CLI, 4 months ago: "…"`).
- **Temporisation de 20 minutes et exclusion des sous-agents** : évite le bruit répété dans un même projet et épargne le budget des sous-agents.
- **Échecs de commande et décisions sur fichiers** : joint les correctifs passés et notes de fichiers pertinents.
- **Pré-compaction** : sauvegarde la tâche en cours, les conclusions et les commandes non terminées.

## Options

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(Clé `"plugins"` pour opencode 2.x.)

- `autoRecall` : injection automatique (`true` par défaut).
- `tools` : mise à disposition des 6 outils (`true` par défaut).
- `bin` : chemin explicite vers `deja`.

Pour activer MCP (8 modes : `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) sur les 49 outils compatibles, lancez `deja install --auto` (voir [changer d'agent sans perdre le contexte](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Résolution du binaire

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> copie intégrée au paquet npm.

## Confidentialité

Index stocké dans `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` possible) avec masquage automatique des clés `sk-*`, `ghp_*`, `AKIA*` et `Bearer`. Tout reste hors ligne sans configuration explicite de `deja embed`.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 50 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## ## Licence

MIT
