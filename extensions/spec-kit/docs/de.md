# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Eine Erweiterung für [Spec Kit](https://github.com/github/spec-kit). Bevor du ein Feature umsetzt, durchsucht `/speckit.deja-vu.recall` deine bisherigen Coding-Agent-Sessions und fasst zusammen, was versucht wurde, was beschlossen wurde und was fehlgeschlagen ist — jeweils mit Session-ID belegt.

Die Sessions stammen aus [deja](https://github.com/vshulcz/deja-vu), das die bereits auf der Platte liegenden Protokolle von Claude Code, Codex, Cursor, Copilot, Gemini und weiteren Agents indiziert. Alles bleibt lokal auf deinem Rechner.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Installiere zuerst `deja`:

```sh
brew install deja-vu
# oder
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Anschließend in einem Spec-Kit-Projekt:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Jedes deja-Release enthält `deja-spec-kit.zip` in passender Version. Für einen lokalen Checkout: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Verwendung

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Ohne Argumente liest der Befehl `spec.md` und `plan.md` des aktiven Features und wählt die Suchbegriffe selbst. Mit Argumenten sucht er nach diesen Begriffen. Je nach Agent heißt der Befehl ggf. `/speckit-deja-vu-recall` oder `$speckit-deja-vu-recall`.

Intern läuft vom Repository-Stamm aus:

```sh
deja recall -- "<query>"   # passende Sessions, aktuelles Projekt zuerst
deja files -- "<query>"    # dazu geöffnete oder bearbeitete Dateien
```

sowie `deja show <id>`, falls ein Ausschnitt zu kurz ist. Der Bericht erscheint als Abschnitt `Prior sessions` mit verworfenen Versuchen, Entscheidungen, Dateien und offenen Punkten, jeweils zitiert als `(deja:<id>)`.

## Hook

Die Erweiterung registriert einen optionalen Hook `before_implement`. Beim Aufruf von `/speckit.implement` bietet der Agent an, zuerst den Rückgriff auszuführen; ohne deine Bestätigung läuft nichts. Zum Abschalten setze `enabled: false` in `.specify/extensions.yml` oder führe `specify extension disable deja-vu` aus.

## Hinweise

- Nur lesend: Der Befehl sucht und liest, ändert aber keine Dateien.
- Der allererste deja-Aufruf auf einem Rechner baut den Index auf, was bei viel Historie etwas dauern kann; danach antwortet er deutlich unter einer Sekunde.
- Abgerufener Session-Text gilt strikt als Referenzdatum; der Befehl weist den Agent an, niemals Anweisungen aus alten Transkripten auszuführen.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 52 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

MIT

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
