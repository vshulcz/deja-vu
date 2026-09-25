<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Une seule mémoire pour tous vos agents de code, construite à partir de l'historique déjà présent sur votre disque.</b></p>

<p align="center">Votre agent s'apprête à redéboguer quelque chose que vous avez corrigé en mars, dans un autre
agent. deja indexe les sessions que Claude Code, Codex, Cursor et tous les autres agents de cette machine
écrivent déjà sur le disque, et rend la bonne à celui qui pose la question.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="la même question au même agent deux fois : sans mémoire il ne se souvient de rien, avec deja il répond avec une conclusion vieille de huit mois"></p>

<p align="center"><sub><em>Personne n'a cherché : l'agent a appelé deja de lui-même. Deux exécutions réelles, vrai modèle, vrais appels d'outils, sur un corpus synthétique, donc l'historique de personne n'est publié.</em></sub></p>

<p align="center"><b>deja est pleine dès la première minute : l'historique que 34 agents ont déjà écrit, indexé en quelques secondes, sans modèle et sans étape de collecte à part.</b></p>

<p align="center">
<b>58–80 % de tokens en moins</b> sur une tâche que cette machine avait déjà résolue &middot; <b>88.1 % hit@1</b> sur LongMemEval-S (jeu nettoyé de 470 questions) &middot; <b>70.5 %</b> sur LoCoMo &middot; des requêtes en <b>millisecondes</b> sur des gigaoctets d'historique<br>
<sub>Onze exécutions par bras : 53,558 tokens contre 126,222 sans rien de branché, et 24,068 contre 119,461 sur une version ultérieure, là aussi onze exécutions &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">ce que coûte une tâche</a> &middot;
les deux harnais de recherche sont dans ce dépôt et tournent sur des jeux de données publics en quelques minutes &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">vérifiez les chiffres vous-même</a></sub>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | Français | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Documentation</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Comparaison</a></p>
<p align="center"><sub>Si ça vous sert, mettez une étoile à deja-vu sur <a href="https://github.com/vshulcz/deja-vu">GitHub</a>.</sub></p>

## Installation

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Si `raw.githubusercontent.com` n'est pas joignable, il existe un miroir npm dans la même version :

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Dix secondes pour installer, une dizaine pour indexer. La seconde commande branche le rappel MCP sur chaque
agent qu'elle trouve, active le rappel au démarrage de session là où l'agent le permet, et construit le premier
index, pour que la session suivante n'attende rien.

Ouvrez une nouvelle session d'agent et posez une question sur quelque chose d'il y a des mois :

> on a déjà bossé sur jwt refresh rotation ? regarde dans ta mémoire

Vous n'avez même pas besoin de demander : avec le rappel automatique, l'agent sait dès l'ouverture de la
session ce qui a été résolu dans ce projet.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw et pi ont aussi un paquet dans leur
propre écosystème, pour qui a l'habitude d'installer ses extensions par là :

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed : cherchez deja dans le panneau des extensions
# Kimi Code : /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI : codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build : grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` branche déjà tout ce qui précède, donc l'un ou l'autre chemin suffit. Les deux ensemble
ne gênent pas non plus : chaque paquet lit ce qu'a écrit `deja install` et ne complète que ce qui manque, sans
enregistrer les outils en double ni rappeler deux fois. Les détails sont dans [`extensions/`](../../extensions).

La même recherche existe comme skill, installable par n'importe quel agent qui lit `SKILL.md` :

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI : Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

Le skill appelle le binaire `deja` que vous avez installé, il n'en embarque pas un à lui.

Autres moyens : `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, ou `npx @vshulcz/deja-vu "requête"` pour essayer sans
rien installer. Sous Windows, le script d'installation s'arrête sur `unsupported OS` : c'est un script shell.
Prenez `deja-vu_<version>_windows_amd64.zip` dans la
[dernière release](https://github.com/vshulcz/deja-vu/releases/latest) et mettez `deja.exe` dans le `PATH`.

Le binaire seul est déjà une installation complète : indexation, recherche, `show`, `ctx`, `blame`, `--json` et
le nettoyage des identifiants n'ont besoin de rien d'autre. Ce que fait `deja install`, c'est brancher MCP sur
vos agents et activer le rappel au démarrage de session. Ça vaut le coup, mais c'est facultatif.

## Ce que ça apporte

**Résolu dans Codex, retenu par Claude.** Trente-quatre agents de code écrivent chaque conversation dans des
fichiers locaux, et deja transforme ces fichiers en une couche de mémoire qu'ils peuvent tous lire.

| | |
| --- | --- |
| **Recherche vers le passé** | `deja "connection pool exhausted"` parcourt des gigaoctets, y compris tout ce qui précède l'installation de deja. Une question en langage naturel bascule sur le mode pertinence. Le temps est un indice, pas un filtre. |
| **Rappel entre agents** | L'outil MCP `deja` en mode `recall` répond « on a corrigé ça il y a trois semaines » depuis n'importe quel agent, peu importe qui l'avait corrigé. |
| **Survit au compactage** | Mesuré sur 43 compactages : le résumé a gardé 77 % des décisions et 0.2 % des commandes que vous avez lancées. Les 99.8 % restants, deja les rend. Dans Claude Code et Codex, deja note la tâche, les fichiers et les commandes au moment où le compactage commence, et les restitue d'un bloc à la session suivante. |
| **Rappel au moment d'agir** | Avant que l'agent modifie un fichier ou lance une commande, le hook `PreToolUse` dit ce qui avait été décidé sur ce fichier, quelle forme de cette commande marche ici, ou que le programme n'existe tout simplement pas sur cette machine. Quand une commande échoue, le hook `PostToolUse` montre ce qui a été lancé après la même erreur sur cette machine : exactement la paire que l'agent ne demandera pas. |
| **Indexe le travail, pas seulement les mots** | Chaque fichier ouvert à chaque tour, chaque commande lancée avec son code de sortie, et le fragment exact qu'une édition a remplacé. Précisément ce que tout résumé perd. |

En plus : `deja promote <id> --state rejected` marque une décision annulée, et dès lors chaque résultat montre
qu'elle a été tentée puis écartée ; un résultat signale que « 4 fichiers de cette session ont changé depuis » et
se tait quand il ne peut pas trancher ; `deja sync ssh laptop` déplace la mémoire entre machines par simple
ajout, sans nuage au milieu ; `deja handoff --to codex` emballe le contexte courant pour continuer dans un autre
agent ; les clés, jetons, JWT et blocs de clé privée de formes connues sont retirés à l'indexation, sachant que
faire correspondre un motif n'est pas détecter un secret et qu'une forme inconnue peut passer.

### Dessinez votre propre travail

`deja stats --card` dessine directement dans le terminal ; donnez-lui un nom de fichier et il écrit un SVG pour
le README de votre profil. Pour le publier ailleurs,
[convertissez-le en PNG](https://vshulcz.github.io/deja-vu/card/) : cette page convertit dans votre propre
navigateur.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="carte de statistiques deja : une année de sessions en carte de chaleur, de quels agents elles viennent, et la plus longue"></p>

La référence complète est sur le [site de documentation](https://vshulcz.github.io/deja-vu/).

## Vie privée

L'indexation et la recherche sont locales. Seuls `deja update`, `deja sync ssh`, la vérification de version
dans `deja doctor` et `deja embed`, qui va vers l'endpoint que vous configurez, touchent au réseau.

Les identifiants sont nettoyés à l'indexation : clés AWS, affectations `api_key=` et `token=`, jetons bearer et
JWT nus, blocs PEM de clé privée, jetons de divers fournisseurs, URL de forme `scheme://user:pass@host`,
valeurs à forte entropie qu'aucune règle ne couvre, et mots de passe écrits en prose — « the admin password
is … », là où il n'y a aucun séparateur sur lequel s'appuyer. La valeur devient `[redacted:<kind>]` et le texte
autour reste cherchable. `deja share` et `deja sync export` nettoient une seconde fois à l'export. Faire
correspondre un motif n'est pas détecter un secret : une forme que les règles ne connaissent pas peut passer
telle quelle, voir le modèle de sécurité.

`deja forget` retire des sessions de l'index reconstruit et laisse une pierre tombale, de sorte que le
`deja index` suivant ne peut pas les faire revenir depuis l'historique d'origine.
Le [modèle de sécurité](../../docs/SECURITY-MODEL.md) documente les flux de données, les limites du nettoyage, les
hypothèses de confiance et la vérification des releases.

## Ligne de commande

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Commande | Ce qu'elle fait |
| --- | --- |
| `deja <requête>` | Cherche dans tout l'historique. Plusieurs mots valent ET, les guillemets exigent un texte contigu ; sans correspondance exacte, elle essaie les formes du mot et les orthographes proches. |
| `deja wip` | Ce que faisait la dernière session dans ce répertoire : la tâche, ce qui a été tranché, les fichiers en main, la dernière commande et si elle a échoué. Tout est déduit des enregistrements, sans dépendre de notes prises par quelqu'un. |
| `deja blame <chemin>[:ligne]` | Quelles sessions ont discuté ce fichier, ce qui a été décidé et pourquoi. Avec un numéro de ligne : le commit qui l'a modifiée en dernier, et la session qui a écrit le texte que ce commit a supprimé. |
| `deja files <sujet>` | Dans l'autre sens : quels fichiers le travail sur un sujet a réellement touchés. |
| `deja how <outil>` | Comment cela se lance vraiment sur cette machine, avec de vrais arguments, tirés des commandes que les agents ont déjà exécutées. |
| `deja fix <erreur>` | Ce qui a été lancé après la même erreur sur cette machine, et après quoi l'erreur n'est pas revenue. |
| `deja friction` | Les erreurs qui touchent plus de trois sessions différentes, avec les outils d'où elles viennent. |
| `deja ctx <requête>` | Résumé Markdown des meilleurs résultats, prêt à être collé dans un prompt. |
| `deja resume <id>` | Rouvre la session trouvée dans l'outil auquel elle appartient. |
| `deja view` | Exporte toute la mémoire dans un seul fichier HTML local. Pas de serveur, rien ne quitte la machine. |
| `deja doctor [--deep]` | Auto-diagnostic ; avec `--deep`, vérifie l'index contre les fichiers source. |
| `deja mcp` | Le serveur MCP sur stdio, celui-là même que branche `deja install`. |

La référence complète est dans la [documentation des commandes](https://vshulcz.github.io/deja-vu/guide/commands.html).

### Outils MCP

Le serveur expose un seul outil, `deja`, et le paramètre `mode` choisit la capacité : `recall`, `context`,
`blame`, `fix`, `how`, `remember`. `deja install` le branche tout seul, donc cela n'importe que si vous
configurez un agent à la main. Les six anciens noms d'outils continuent de fonctionner chez les clients déjà
branchés.

Un outil au lieu de sept est une question de coût, pas de style. Un serveur MCP branché envoie les définitions
de ses outils à chaque requête, donc elles sont payées à chaque tour, que l'agent ait appelé quelque chose ou
non : 477 tokens ici, contre 8,283 pour le plus gros des huit serveurs mesurés. Celui de deja était à 828
jusqu'à ce que le schéma soit réduit à un outil avec des modes.

## Outils pris en charge

Avec le rappel automatique, Claude Code et Codex confient à deja l'enregistrement courant au début du
compactage, et deja garde ce que le résumé est sur le point de perdre : la tâche, les conclusions, les fichiers,
ce qu'a donné chaque commande, et ce qui reste à faire. Le hook suivant, dans la même session et le même
répertoire de travail, le rend d'un bloc, sans dépasser 4 Ko, avec une ligne indiquant si le dépôt a changé
depuis. `deja stats` compte les appels d'outils entre le compactage et la première édition, et c'est avec ça que
la chose a été mesurée.
Les détails sont dans [récupération après compactage](../../docs/compaction.md).

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

Ce que chacun prend en charge — rappel MCP, rappel automatique, skills, commandes, resume, handoff — se trouve
dans la [matrice des capacités du README anglais](../../README.md#supported-harnesses). Les emplacements de stockage
personnalisés se déclarent avec les variables `DEJA_*_ROOT`, et les variables de migration propres à chaque
outil sont également respectées.

### Agents avec leur propre paquet

`deja install --auto` branche ceux-ci comme les autres, et c'est toujours le chemin le plus court. Chacun a par
ailleurs un paquet dans son écosystème, pour qui installe ses extensions de ce côté-là :

| Agent | Paquet | Installation |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | plugin `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, puis `codex plugin add deja-vu@deja-vu` |
| Grok Build | plugin `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, puis `grok plugin install deja` |
| OpenClaw | ClawHub et npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (et omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

L'un ou l'autre suffit et les deux ensemble ne cassent rien : chaque paquet lit d'abord ce que `deja install` a
écrit. opencode, dsh et OpenClaw ne complètent que ce qui manque ; Kimi, Grok, Codex et pi s'effacent si
l'installeur a déjà branché ; dans Zed, les deux côtés utilisent le même id de serveur. L'ordre d'installation
n'a donc pas d'importance.

Tous utilisent le deja que vous avez déjà installé ; la copie embarquée dans le paquet n'est qu'un secours.

## Rappel sémantique, en option

Pointez `deja embed` vers un Ollama local, LM Studio ou tout endpoint compatible OpenAI via `DEJA_EMBED_URL`, et
poser la question avec d'autres mots touche aussi. Sans runtime disponible, la recherche lexicale et le rappel
MCP fonctionnent comme d'habitude.

## Preuves

```sh
deja bench recall     # plancher de régression du classement : 100 requêtes, la moitié en russe, la CI échoue si le rappel baisse
deja bench context    # 30 chaînes de tâches avec graine, plus cinq contrôles négatifs
deja bench block      # si la réponse est encore dans le fragment de texte remis
deja bench prompt     # quand le hook par prompt parle, et quand il parle à tort
deja bench ingest     # coût d'une mise à jour de l'index : rien n'a changé, un tour ajouté, un fichier en plus, réécriture complète
```

L'expérience de contexte compare le rappel de deja à l'historique complet, à un grep naïf et au démarrage à
froid. Avec la graine par défaut :

| Approche | Tokens (médiane) | Couverture (médiane) | Tokens du contrôle négatif |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

La même couverture factuelle qu'un grep sur les logs bruts avec environ 250 fois moins de tokens ; environ 70
fois moins que les sessions trouvées par relecture intégrale ; et rien d'injecté dans les chaînes sans
historique pertinent. Le générateur de corpus et l'étiquetage de pertinence sont du Go ordinaire et relisible.
Avant de croire un chiffre, regardez comment il définit « pertinent », y compris les nôtres.

Mesuré sur un vrai dépôt : 2,419 sessions, 179k messages, 1.9 Go d'enregistrements.

| Mesure | Résultat |
| --- | --- |
| Requête dans le processus | médiane **0.7–0.8 ms**, environ 19 ms sur les meules de foin de LongMemEval-S |
| `deja <requête>` de bout en bout | médiane d'environ 0.2 s sur ce dépôt : démarrage du processus, contrôle de fraîcheur de tous les stockages, classement, affichage |
| Contrôle de fraîcheur seul | environ 50 ms quand rien n'a changé |
| Taille de l'index | 200 Mo, environ 10 % du corpus |

L'index est incrémental. Quand un fichier de session s'allonge, seul ce fichier est relu.

## Comment ça marche

Un index inversé local dans `~/.cache/deja` : il analyse les stockages JSONL et SQLite, nettoie les
identifiants, écrit `records.bin` et des seaux de mots, et garde l'état de chaque fichier dans `manifest.gob`,
si bien qu'une seconde exécution n'ingère que ce qui a changé. Le serveur MCP, les statistiques, share et sync
lisent ce même index. Les détails sont dans [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Questions fréquentes

**Est-ce que quelque chose quitte ma machine ?** Non, sauf si vous le demandez. Voir
[flux de données](../../docs/SECURITY-MODEL.md#data-flows).

**Et les secrets déjà présents dans les logs ?** Ils restent dans les fichiers de l'outil d'origine, ce sont les
données de votre agent. Ils n'entrent ni dans l'index de deja, ni dans les résumés, ni dans share, ni dans
l'export de sync.

**Est-ce que ça va ralentir mon agent ?** Un rappel est une requête lexicale sur un index local : médiane de
0.7–0.8 ms, et rien n'attend un modèle. Le hook ajoute le démarrage du processus et le contrôle de fraîcheur
des stockages, soit des dizaines de millisecondes sur un dépôt de plusieurs gigaoctets.

**Dois-je changer ma façon de travailler ?** Non. C'est l'agent lui-même qui appelle le rappel ; avec le rappel
automatique, il sait dès l'ouverture de la session ce qui avait été décidé dans ce projet.

**En quoi est-ce différent des autres outils de mémoire ?**

| | deja | Plateformes de mémoire<br>(Mem0, Letta, memU) | Recherche de sessions<br>(cass) |
| --- | :-: | :-: | :-: |
| Connaît le travail antérieur à son installation | oui | non | oui |
| Nécessite une étape de collecte | non, la transcription est la mémoire | l'agent ou votre code écrivent des faits | non |
| Nécessite une clé LLM ou embeddings | non | oui | facultatif |
| Rappelle sans qu'on le lui demande | au démarrage de session et avant l'exécution d'un outil | non | non |

La [comparaison complète](https://vshulcz.github.io/deja-vu/guide/compare.html) en couvre onze.

**Où est l'historique de sessions de Claude Code, et peut-on le chercher ?** Dans `~/.claude/projects`, un
fichier JSONL par session ; Codex dans `~/.codex/sessions`, Cursor dans le SQLite `state.vscdb`. `deja search`
les lit sur place, `deja last` liste la session la plus récente de chaque agent, et `deja view` ouvre tout
l'historique comme une page locale. Les chemins par agent sont dans
[où sont stockées les sessions](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

**Mon historique Claude Code a disparu, est-il perdu ?** Claude Code supprime les enregistrements de plus de 30
jours (`cleanupPeriodDays` dans `~/.claude/settings.json`), et `claude --resume` ne liste que ce qui reste. Les
sessions que deja a indexées avant le nettoyage restent cherchables après la disparition du fichier. Les
détails sont dans
[fichiers de session sur disque](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

**Comment tout effacer ?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Guides

Écrits par situation, pas par fonctionnalité :

- [Un agent de code se souvient-il des conversations précédentes ?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — ce que chaque agent laisse entre les sessions et ce qu'il perd
- [Fichiers de session sur disque](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — jusqu'où grossit `~/.claude/projects` et ce que coûte la suppression
- [L'agent vient de perdre votre contexte](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — après un plantage, après un clear, ou quand la session revient vide
- [La fenêtre de contexte est pleine](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — ce que le compactage préserve vraiment, mesures à l'appui, et ce qu'on peut faire à la place
- [Reprendre la session d'hier](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — la retrouver parmi tous les agents et la rouvrir dans le sien
- [L'agent a refait une erreur que vous aviez corrigée](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Retrouver la session où ça a été résolu](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Pourquoi les agents oublient entre les sessions](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [où chaque agent range son historique](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [Ce que le compactage perd](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [changer d'agent](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [auditer ce qu'ont fait les agents](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [exporter une conversation](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [entre machines](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [ce que la mémoire coûte en tokens](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

Par outil : [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Essayez sur votre propre historique

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Dix secondes pour installer, une dizaine pour indexer. La prochaine fois qu'un agent ouvrira une session, il
saura déjà ce que vous avez résolu dans ce projet, y compris avant que vous n'installiez deja.

## Développement

`make build test lint`, puis [CONTRIBUTING.md](../../CONTRIBUTING.md).
Un nouvel agent commence par le [registre des parseurs](../../docs/ARCHITECTURE.md#source-parsers).
Les priorités et ce que nous ne ferons pas sont dans [ROADMAP.md](../../ROADMAP.md).

## Licence

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
