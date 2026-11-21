# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indexe les fichiers de session déjà écrits sur disque par les agents de code — Claude Code, Codex, Cursor, opencode et 48 autres — et répond à partir d'eux. Cette extension expose cet index au panneau agent de Zed comme serveur de contexte, permettant à un fil de retrouver vos travaux passés, même antérieurs à l'installation de deja.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Ce que reçoit l'agent

Un seul outil, `deja`, appelé avec un paramètre `mode` :

| Mode | Ce qu'il fournit |
|---|---|
| `recall` | Sessions correspondant à une erreur, un nom de fonction, un chemin ou un drapeau. |
| `context` | Le résumé complet d'une session passée lorsque le raisonnement importe. |
| `blame` | Les sessions ayant traité d'un fichier avant modification ou suppression. |
| `fix` | Ce que cette machine a exécuté après la même erreur auparavant. |
| `how` | La vraie commande de build, test ou déploiement exécutée ici. |
| `orient` | Les commandes lancées et les fichiers touchés sur ce projet. |
| `remember` | Enregistre une décision durable pour un rappel ultérieur. |
| `handoff` | L'état d'une autre session pour reprendre là où elle s'est arrêtée. |

## Installation

Installez l'extension depuis le catalogue de Zed. Au premier appel, elle demande à Zed d'exécuter `deja --version` et réutilise le `deja` du `PATH` de Zed si présent. Sinon, elle télécharge le binaire de release dans son propre dossier. Comme une extension s'exécute en sandbox sans accès aux chemins système habituels, un binaire `deja` absent du `PATH` de Zed doit être déclaré :

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` configure également Zed directement dans `settings.json` — la voie la plus courte si vous disposez de la CLI. Les deux utilisent le même identifiant `deja-context-server` et Zed déduplique par identifiant : quel que soit l'ordre d'installation, une seule entrée subsiste. `deja uninstall zed` supprime l'entrée ajoutée par la CLI.

L'indexation et la recherche restent locales : aucun appel réseau, et les identifiants sensibles sont caviardés à l'indexation.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 52 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

MIT, comme deja.

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
