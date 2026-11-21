# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Un fournisseur de mémoire pour Hermes qui répond à partir des sessions de code déjà présentes sur cette machine : Claude Code, Codex, Cursor, Hermes lui-même et tous les autres agents lus par deja, y compris les mois antérieurs à toute installation. Aucun modèle requis, aucune clé API, aucun serveur : tout est indexé localement par [deja](https://github.com/vshulcz/deja-vu), un unique binaire Go.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

`deja` doit d'abord se trouver dans le `PATH` :

```bash
brew install deja-vu        # ou : curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # connecte Hermes et tous les autres agents supportés sur cette machine
hermes plugins install deja-memory   # ou depuis GitHub : hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

ou choisissez `deja-memory` dans `hermes memory setup`, qui vérifie la présence du binaire avant d'activer le fournisseur.

## Ce qu'il fait

- **Avant le premier tour d'une session** : fournit à Hermes le condensé transmis par les hooks deja à Claude Code et Codex — les travaux récents dans ce projet et ce qui a été validé. Ensuite, il n'intervient que si la demande actuelle correspond à du passé ; la plupart du temps il n'ajoute rien.
- **Visibilité sur ce qui est rappelé** : les mentions affichées dans Claude Code (le reçu indiquant l'agent d'origine, ce que le début de session a ramené, les notes de construction et hebdomadaires) empruntent la ligne de rappel d'Hermes `🧠 deja · …` dans la CLI interactive et `hermes chat -q`. La réponse du modèle ne change pas. Hermes 0.17 n'a pas de ligne de rappel, `hermes -z` et `hermes chat -Q` n'en affichent aucune, la TUI ne la fait qu'effleurer dans la barre d'état et une passerelle peut exposer un salon de groupe : deja garde donc ses notes pour une session qui sait les afficher.
- **Pas de rappel automatique sur `cron`** : lorsque Hermes indique la plateforme `cron`, le fournisseur n'ajoute rien tout seul, mais les outils deja répondent toujours s'ils sont appelés.
- **Trois outils** : `deja_recall` recherche les sessions passées, `deja_fix` indique ce qui a été exécuté après une erreur donnée et `deja_blame` liste les sessions ayant édité un fichier.
- **Notes priorisées** : toute entrée ajoutée par Hermes dans `MEMORY.md` ou `USER.md` devient aussi une note deja et se classe avant les sessions ordinaires.
- **Compactage suivi** : conserve ce que la session était en train de faire pour le tour suivant, puis retire son marqueur actif en fin de session.

Une chose qu'un fournisseur de mémoire ne peut pas faire seul : coller l'ancien correctif juste à côté d'une commande qui vient d'échouer. Cela passe par `transform_tool_result`, un hook de plugin alors qu'Hermes passe au fournisseur de mémoire un contexte où `register_hook` ne fait rien (`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` installe le plugin de hooks qui s'en charge.

Il ne réécrit jamais les tours Hermes : Hermes les enregistre déjà et deja lit ce magasin au rafraîchissement suivant.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 52 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Même code que `deja install`

`deja install hermes-auto` écrit ce fournisseur dans `~/.hermes/plugins/deja-memory` ainsi que le serveur MCP et un plugin de hook `pre_llm_call`. Ces fichiers proviennent exactement de la même source (`cmd/deja/install_hermes_memory.go`), à la seule différence que cette copie cherche `deja` sur le `PATH` au lieu de coder le chemin en dur. `TestHermesExtensionIsTheProviderInstallWrites` échoue dès que les deux divergent.

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
