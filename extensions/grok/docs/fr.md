# deja pour Grok Build

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Mémoire locale persistante issue de l'historique de vos sessions Claude Code, Codex, Cursor, opencode, Zed et 44 autres agents de code. Exécutée localement par le binaire Go unique `deja` (`DEJA_OFFLINE=1` coupe la vérification quotidienne).

## Installation

Installez d'abord le binaire `deja` (le plugin ne contient pas d'exécutable natif) :

```sh
brew install deja-vu
# ou :
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Ajoutez ensuite le catalogue et installez l'extension ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)) :

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

Sinon, `deja install --auto` écrit directement `[mcp_servers.deja]` dans `~/.grok/config.toml` et `~/.grok/hooks/deja.json`.

## Ce qui est inclus

- **Outil MCP `deja`** (8 modes : `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **Compétence `deja-history`** et commande **`/deja:recall <query>`**
- **5 hooks (`hooks/recall.mjs`)** : `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (sur `Bash` avec Grok 1.0.41), `PreCompact` et `SessionEnd`

## Aucun doublon avec l'installateur CLI

Dès que `[mcp_servers.deja]` est déjà présent dans `~/.grok/config.toml` ou que `~/.grok/hooks/deja.json` déclare déjà les hooks deja, le plugin s'efface automatiquement (`deja uninstall grok` retire la configuration locale).

## Résolution du binaire

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## Désactivation propre en absence de binaire

S'appuie sur `${GROK_PLUGIN_ROOT}` et `.mcp.json`. Avec Grok Build 1.0.5 sans `deja` installé, `grok mcp doctor` et `grok inspect` affichent `handshake failed: connection closed` jusqu'à l'installation du binaire.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 51 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## ## Licence

MIT
