# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Lokales Gedächtnis für [pi](https://github.com/badlogic/pi-mono) aus den bereits auf Festplatte liegenden Sitzungsprotokollen von 52 weiteren Coding-Agenten (darunter Claude Code, Codex, Cursor, Gemini, OpenClaw und Hermes). Die Einzel-Binary `deja` arbeitet vollständig mit lokalem BM25-Index ohne LLM oder Embeddings; Netzwerkzugriffe erfolgen nur bei expliziten Update-/Sync-Befehlen sowie beim per `DEJA_OFFLINE=1` abschaltbaren täglichen Versionscheck.

## Installation

```sh
pi install npm:@vshulcz/pi-deja
```

Ist `deja` noch nicht separat installiert, greift das Paket gemäß [Welche Binary verwendet wird](#welche-binary-verwendet-wird) auf die mitgelieferte Kopie zurück. Alternativ vorab über Homebrew installieren:

```sh
brew install deja-vu
```

Erkennt das Paket eine bereits über `deja install pi-auto` oder `deja install --auto` eingerichtete Eigeninstallation, tritt es automatisch zurück, sodass nichts doppelt eingebunden wird. Dieselbe Anbindung funktioniert über `pi.extensions` und `before_agent_start` auch unter `omp`.

## Funktionsweise

- **Sitzungsstart (Statusleiste in der Fußzeile)**: zeigt indexierte Sitzungen und Treffer im aktuellen Projekt.
- **Bei jedem Prompt (`before_agent_start`)**: sucht passende frühere Projektentscheidungen und hängt sie vor der Modellantwort an.
- **Nach fehlgeschlagenem Befehl (`tool_result` bei `bash` / `powershell`)**: liefert den Befehl nach, der exakt diesen Fehler früher behoben hat.
- **Vor Lese-/Schreibaktionen (`tool_result` bei read/edit/write)**: erinnert an frühere Festlegungen zur jeweiligen Datei.
- **Nach Komprimierung (`session_compact`)**: stellt Aufgabe, Beschlüsse und offene Befehle nach dem Compact-Summary wieder her.
- **Sitzungsende (`session_shutdown`)**: aktualisiert den lokalen Suchindex.
- **`/deja <query>`**: durchsucht alle 53 unterstützten Werkzeuge direkt aus der Eingabezeile.

## Welche Binary verwendet wird

Die Suche nach `deja` erfolgt in dieser Reihenfolge:

1. Pfad in `DEJA_BIN`
2. `deja` im `PATH`
3. Übliche Installationsorte (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` usw.)
4. Mitgelieferte Binary aus `@vshulcz/deja-vu`

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 53 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Datenschutz

Der Index liegt in `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` überschreibbar); Geheimnisse wie `sk-*`, `ghp_*`, `AKIA*` und `Bearer` werden beim Indexieren entfernt. Ohne explizites `deja embed` verlässt kein Inhalt den Rechner. Über `deja install --auto` lassen sich `recall`, `fix`, `blame` und `handoff` in allen 53 Agenten aktivieren.

## Lizenz

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
