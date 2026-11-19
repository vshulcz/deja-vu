# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Une extension pour [Spec Kit](https://github.com/vshulcz/deja-vu). Avant d'implémenter une fonctionnalité, `/speckit.deja-vu.recall` interroge vos anciennes sessions d'agents de code et résume ce qui a été tenté, décidé et ce qui a échoué, avec l'identifiant de session associé à chaque point.

Les sessions proviennent de [deja](https://github.com/vshulcz/deja-vu), qui indexe les transcriptions déjà écrites sur disque par Claude Code, Codex, Cursor, Copilot, Gemini et d'autres agents. Tout reste sur votre machine.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Installez d'abord `deja` :

```sh
brew install deja-vu
# ou
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Puis, dans un projet Spec Kit :

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Chaque release de deja fournit `deja-spec-kit.zip` à la même version. Pour tester une copie locale : `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Utilisation

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Sans argument, la commande lit le `spec.md` et le `plan.md` de la fonctionnalité active et choisit ses requêtes. Avec des arguments, elle recherche ces mots. Le nom de la commande suit les conventions de votre agent et peut apparaître sous la forme `/speckit-deja-vu-recall` ou `$speckit-deja-vu-recall`.

Sous le capot, elle exécute depuis la racine du dépôt :

```sh
deja recall -- "<query>"   # sessions correspondantes, ce projet en priorité
deja files -- "<query>"    # fichiers ouverts ou modifiés sur ce sujet
```

ainsi que `deja show <id>` lorsqu'un extrait est trop court pour comprendre le contexte. Le rapport prend la forme d'une section `Prior sessions` structurée en pistes échouées, décisions, fichiers et points ouverts, chaque point étant cité sous la forme `(deja:<id>)`.

## Hook

L'extension enregistre un hook optionnel `before_implement`. Lorsque vous lancez `/speckit.implement`, l'agent propose d'exécuter le rappel au préalable ; rien ne s'exécute sans votre accord. Pour le désactiver, ajoutez `enabled: false` dans `.specify/extensions.yml` ou lancez `specify extension disable deja-vu`.

## Remarques

- Lecture seule : la commande recherche et lit sans rien modifier.
- Le premier appel à deja sur une machine construit l'index, ce qui peut prendre un moment sur un gros historique. Ensuite il répond bien en dessous d'une seconde.
- Le texte rappelé est traité comme donnée de référence ; la commande indique à l'agent de ne jamais suivre d'instructions trouvées dans ces archives.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 49 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

MIT

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
