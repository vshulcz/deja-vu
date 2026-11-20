# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | Français | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Fait partie de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** sur LongMemEval-S) · [Documentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Démo en ligne](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indexe les fichiers de session déjà écrits sur disque par les agents de code — Claude Code, Codex, Cursor, opencode et 45 autres — et répond à partir de ces données. Ce plugin intègre cet index dans Kimi Code : le rappel arrive avec votre invite et l'agent peut explorer l'historique lui-même.

![Démo de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Cette commande épingle la dernière release et enregistre sa source pour la vérification des mises à jour dans Kimi. Une archive `kimi-deja.zip` (~10 Ko au lieu de tout le dépôt) est également publiée pour les catalogues ou installations allégées.

Installez d'abord le binaire si besoin (et câblez éventuellement les autres agents du poste) :

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Les plugins étant chargés depuis leur copie gérée, exécutez `/reload` ou `/new` après l'installation.

## Mises à jour

Kimi ne notifie les mises à jour que pour les plugins issus de son propre marketplace. Pour une installation depuis le dépôt, relancer `/plugins install` récupère la version courante, et `deja doctor` vous prévient quand la copie installée retarde par rapport à celle fournie par ce `deja` :

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## Ce que vous obtenez

- **Les hooks installés par `deja install kimi-auto`** : dès le premier prompt d'une session, les acquis du projet (`deja hook-context --once`). À chaque prompt, un rappel ciblé (`deja hook-prompt`) ; Kimi l'ajoute au tour, sans rien ajouter s'il n'y a aucun résultat pertinent. Avant compactage, il lit les tours sur le point d'être repliés pour préserver l'activité en cours (`deja hook-precompact`). À `SessionEnd`, le marqueur actif de la session disparaît (`deja hook-session-end`).
- **Outils** : déclare `deja mcp` comme serveur MCP avec l'outil unique `deja` (`recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` ou `handoff`).
- **`/deja:recall <query>`** pour interroger l'historique directement.
- **La compétence `deja-history`**, chargée au démarrage pour inciter l'agent à vérifier le passé avant de réinvestiguer.

## Quel binaire est lancé

Dans l'ordre : `DEJA_BIN`, le `deja` installé par vos soins (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), puis `deja` dans le `PATH`. Votre propre `deja update` ou `brew upgrade deja-vu` l'emporte toujours.

## Si vous avez aussi exécuté `deja install kimi`

La CLI inscrit le même serveur MCP dans `~/.kimi-code/mcp.json` et le même hook dans `config.toml`. Kimi isole le serveur d'un plugin dans son espace de noms (`plugin-deja:deja`), si bien que les deux tourneraient en double.

Ce plugin détecte la configuration de l'installateur au déclenchement du hook et au démarrage du serveur, puis s'efface — la copie de `deja install` reste toujours à jour. `deja uninstall kimi` rend la main au plugin sans réinstallation.

`deja doctor` indique `plugin` pour kimi quand le plugin assure la liaison, évitant toute fausse alerte.

Deux éléments lui restent invisibles : une entrée MCP ajoutée manuellement sous un autre nom que `deja`, et un hook personnel invoquant deja sans le commentaire marqueur de l'installateur.

## Benchmarks

| Rappel | Tokens | Indexation & recherche |
|---|---|---|
| **97.2% R@5** sur LongMemEval-S (88.9% hit@1 sur 470 questions) | **58% de tokens en moins** (sur des tâches répétées) | **13.6 s** pour indexer 19,195 sessions / **0.7 ms** par requête sur 50 agents de code |

## Mémoire de dépôt & transfert inter-agents

Partagez via git les décisions validées et les impasses du projet dans `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Chaque entrée est caviardée à l'écriture, triée par empreinte avec `merge=union` pour fusionner proprement entre branches parallèles, et chargée chez quiconque clone le dépôt (`{"activations":{"auto":{"repo":false}}}` dans la politique de confiance désactive l'injection par hooks). [En savoir plus](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Vous changez d'outil en cours de route ? Exécutez `deja handoff --to <agent>` (ou utilisez `handoff` via MCP) pour transmettre l'objectif de la session, les décisions, fichiers touchés, vérifications passées et tâches restantes à un autre agent.

## Licence

MIT

Fait partie de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
