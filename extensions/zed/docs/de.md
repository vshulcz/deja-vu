# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indiziert die Session-Dateien, die Coding-Agents bereits auf der Platte ablegen — Claude Code, Codex, Cursor, opencode und 45 weitere — und beantwortet Fragen daraus. Diese Erweiterung stellt diesen Index im Agent-Panel von Zed als Context-Server bereit, sodass Threads auch Arbeit aus der Zeit vor deja finden.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Was der Agent erhält

Ein einziges Tool, `deja`, aufgerufen mit einem `mode`:

| Mode | Antwortet auf |
|---|---|
| `recall` | Sessions passend zu Fehlermeldung, Funktionsname, Dateipfad oder Flag. |
| `context` | Vollständiges Digest einer vergangenen Session, wenn das Warum zählt. |
| `blame` | Sessions, die eine Datei vor dem Bearbeiten oder Löschen behandelt haben. |
| `fix` | Was dieser Rechner früher nach genau demselben Fehler ausgeführt hat. |
| `how` | Der echte Build-, Test- oder Deploy-Aufruf aus früheren Läufen hier. |
| `orient` | Befehle und Dateien früherer Sessions in diesem Projekt. |
| `remember` | Speichert eine dauerhafte Entscheidung zum späteren Abruf. |
| `handoff` | Den Stand einer anderen Session, um nahtlos dort anzuknüpfen. |

## Installation

Installiere die Erweiterung aus der Zed-Erweiterungsliste. Bei der ersten Nutzung lässt sie Zed `deja --version` ausführen und nutzt `deja` aus Zeds `PATH`, wenn das klappt. Andernfalls lädt sie ein Release-Binary von `deja` in ihr eigenes Verzeichnis. Da Erweiterungen im Sandbox-Kontext laufen und übliche Installationspfade nicht absuchen können, muss ein nicht in Zeds `PATH` liegendes `deja` angegeben werden:

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

`deja install --auto` richtet Zed ebenfalls ein und trägt den Server direkt in `settings.json` ein — der kürzere Weg mit vorhandener CLI. Beide nutzen dieselbe ID `deja-context-server`, und Zed ordnet Server nach ID, sodass unabhängig von der Reihenfolge genau ein Eintrag existiert. `deja uninstall zed` entfernt den CLI-Eintrag wieder.

Indizierung und Suche laufen lokal: keine Netzwerkaufrufe, und Zugangsdaten werden beim Indizieren bereinigt.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 50 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

MIT-Lizenz, wie deja selbst.

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
