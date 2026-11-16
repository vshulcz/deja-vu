# `@vshulcz/magpie-deja` — Hook de passerelle Magpie pour deja-vu

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S · **13.6 s** pour indexer 19,195 sessions · **moitié moins de tokens** qu'agentmemory) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Étoile sur GitHub](https://github.com/vshulcz/deja-vu)**

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> Enrichit automatiquement les requêtes traversant [Magpie](https://github.com/yetone/magpie) avec le contexte local issu de 47 autres agents de programmation (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes, etc.).

Fonctionne avec les protocoles OpenAI (`chat`, `responses`), Anthropic (`anthropic`) et Gemini (`gemini`) grâce au binaire Go unique `deja` et son index BM25 local, sans LLM ni embeddings (`DEJA_OFFLINE=1` coupe la vérification quotidienne).

## Installation

```sh
magpie plugin add @vshulcz/magpie-deja
```

Nécessite le binaire `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` ou `install.sh`) et un index local préparé par `deja index`. Pour activer aussi `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` et `handoff` sur les 48 agents pris en charge :

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

Si la requête contient déjà `<deja-memory>` ou `<deja-recall>`, l'extension s'abstient pour éviter toute redondance.

## Fonctionnement et frontière des données

1. **`onRequest` appelle `deja hook-prompt --plain`** : interroge `~/.cache/deja/index.db` (ou `DEJA_INDEX_DIR`) dans la limite de `800ms` et ajoute `<deja-memory>`. **Avertissement sur les requêtes sortantes** : le contexte injecté est transmis au fournisseur de modèle amont avec le reste du prompt.
2. **Cloisonnement via `ctx.cwd`** : avec `allowUnscoped: false`, ne recourt jamais au `process.cwd()` du proxy lors de l'appel à `deja hook-prompt`.
3. **Origine locale par défaut** : `recallRemote: false` limite l'exécution à `ctx.origin === "local"` et `ctx.origin === "magpie-key"`.
4. **Double filtrage des secrets** : nettoyage à l'indexation puis passage dans `maskSecrets` (`deja.hooks.mjs`) ciblant `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, identifiants dans les URL et `api_key=...`.
5. **Anti-boucle et cache de préfixe** : ignore les messages contenant `<deja-memory`, `<deja-recall>` ou `[deja —` tout en préservant le cache de préfixe.

## Options

| Option | Valeur par défaut | Description |
|---|---|---|
| `requestTimeoutMs` | `800` | Délai maximal d'exécution de `deja` (`50`–`5000` ms) |
| `maxBytes` | `4000` | Taille maximale du contexte ajouté (`256`–`32768` octets) |
| `recallRemote` | `false` | Autorise le rappel sur requêtes distantes |
| `allowUnscoped` | `false` | Autorise la recherche sans `ctx.cwd` |
| `cwd` | `""` | Surcharge explicite du répertoire de travail |
| `skipAgents` | `[]` | Agents à exclure de l'injection (ex. `["claude-code"]`) |
| `bin` | `""` | Chemin personnalisé vers `deja` |

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions ; MemPalace publie 96.6%, agentmemory 95.2%) | **58% de tokens en moins** (**moitié moins de tokens** qu'agentmemory sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 48 agents de code (suivant sur 7 : 72 s) |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Utile ? [Mettez une étoile à deja-vu sur GitHub](https://github.com/vshulcz/deja-vu).
