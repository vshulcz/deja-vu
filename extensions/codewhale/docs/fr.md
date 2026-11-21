# deja-vu pour CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Bundle de plugin compatible Claude pour CodeWhale : expose le serveur MCP `deja` (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), la skill `deja-history` et `/deja` sur vos sessions issues de 50 outils de développement IA.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Prérequis : [`deja`](https://github.com/vshulcz/deja-vu#install) installé dans `PATH`.

Installez ce bundle dans CodeWhale depuis le sous-répertoire du dépôt (`extensions/codewhale`) avec `/plugin install`.

Comme le chargeur de compatibilité Claude de CodeWhale importe les serveurs MCP, skills et commandes slash mais ignore `hooks` et `lspServers`, exécutez `deja install codewhale-auto` (ou `deja install --auto`) si vous souhaitez également enregistrer les hooks natifs CodeWhale (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) dans `~/.codewhale/config.toml` (`deja install codewhale` configure uniquement MCP et `tools.always_load`) :

```sh
deja install codewhale-auto
```

## Contenu du bundle

- `.claude-plugin/plugin.json` : métadonnées du plugin (`name: deja-vu`)
- `.mcp.json` : entrée serveur stdio `deja mcp`
- `skills/deja-history/SKILL.md` : instructions de skill pour les modes `deja`
- `commands/deja.md` : commande slash `/deja`

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 52 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

[MIT](../LICENSE)
