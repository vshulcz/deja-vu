# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Fait partie de **[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Plugin `session-query` pour DeepSeek Harness (`dsh`) et DSH Desktop (`anywhere-labs/dsh-desktop`). En plus de vos sessions DSH, il interroge localement les journaux de 48 autres agents de code présents sur la machine (Claude Code, Codex, Cursor, Gemini, Hermes, etc.) via l'exécutable Go unique `deja` et son index BM25 (`DEJA_OFFLINE=1` coupe le contrôle quotidien).

## Installation

Spécifiez impérativement `--profile` lors de l'ajout du plugin :

```sh
dsh plugin --profile web add dsh-deja
# ou directement depuis le dépôt :
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

Si `deja` n'est pas encore installé, définissez `DEJA_BIN` ou configurez les 50 agents et `$DSH_HOME/plugins/deja/` :

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## Ce qu'apporte le plugin

### Six outils agent

| Outil | Rôle |
|---|---|
| `deja_recall` | Rechercher une session passée par message d'erreur, sujet ou option |
| `deja_session` | Lire les tours complets autour d'une correspondance |
| `deja_blame` | Retrouver la session qui a touché un fichier ou `path:line` |
| `deja_fix` | Obtenir la commande qui a corrigé une erreur identique |
| `deja_how` | Revoir les commandes et options utilisées dans le dépôt |
| `deja_remember` | Mémoriser une décision établie |

- **Commande `/deja <...>`** : recherche directe depuis la barre de saisie.
- **Modes pris en charge** : fonctionne avec `deja install dsh-auto`, `dsh web`, `tui`, `headless` et `sdk` (`autoRecall: false` en YAML désactive l'injection automatique).

## Raccourcis et contrôles de recherche

Réglables dans `Settings → Deja` :

- **Mod+F** (ou **Mod+Alt+F** / **Cmd+Option+F** sur macOS) : ouvre la recherche ; restreignez à une session via `/deja --session <bare session UUID> -- <your draft>`.
- **Mod+Shift+K** (**Cmd+Shift+K** sur macOS) : recherche transversale dans tout l'espace.
- **Mod+K** : ouvre le panneau avec le filtre **DSH conversations only** (jusqu'à 20 résultats).
- **Raccourcis navigateurs Linux** : solutions de repli **`Mod+/`**, **`Mod+Shift+,`** et **`Mod+Shift+.`** (`shortcuts.open`).

## Personnalisation des raccourcis

Dans `Settings → Deja`, sur la ligne **`session.search`**, cliquez sur **Change shortcut**, **Restore default** ou **Remove**. L'interface repose sur le **dsh-deja package host plugin** qui expose `/api/deja-search` ; `/api/deja-search?probe=1` retourne `{"ok": true, "plugin": "dsh-deja"}` lorsque `deja search` répond correctement.

## Architecture technique

- **Hooks d'invite** : passe `additionalContext` et `systemMessage` via `ctx.systemPrompt.context` et `agent/pre-step` (`dsh 0.1.1-rc.2`+).
- **Points d'accès et bornes** : `/api/deja-notes` (`--plain`) alimente `client.js` ; `GET /api/deja-search` appelle `deja search --harness deepseek --json --limit 20 -- <query>` avec 20 secondes de délai maximal, 2 MiB de sortie et 500 caractères de requête.
- **Barre latérale** : s'ancre sur `data-slot="sidebar.workspaces"`, `aria-expanded` et `_listArea` (DSH `0.2.1-rc.5`).

## Tester les contrôles de recherche

```sh
node --test extensions/dsh/test/*.test.js
```

Une page autonome sans étape de build est également incluse dans `extensions/dsh/test/search-browser.html` (React 18 CDN).

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 50 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Ce qu'il ne fait pas

L'index reste dans `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), aucun modèle distant n'est appelé hors `deja embed`, les secrets `sk-*`, `ghp_*`, `AKIA*` et `Bearer` sont masqués automatiquement et les journaux d'origine restent intacts.

## Licence

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
