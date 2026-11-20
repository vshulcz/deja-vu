# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Lokales Sitzungsgedächtnis für [opencode](https://opencode.ai) über 48 weitere Coding-Agenten hinweg (u. a. Claude Code, Codex, Cursor, Gemini, Hermes, Zed). Läuft komplett lokal mit der Go-Binary `deja` und BM25 ohne LLM (`DEJA_OFFLINE=1` deaktiviert den täglichen Versionscheck).

## Installation

In `opencode.ai/config.json` eintragen:

```json
{ "plugin": ["opencode-deja"] }
```

(Unter opencode 2.x heißt der Schlüssel `"plugins": ["opencode-deja"]` oder `opencode plugin add opencode-deja`.) Alternativ schreibt `deja install --auto` die Konfiguration nach `opencode.json` und `plugins/deja.js`.

## Funktionsumfang

### Sechs abrufbare Tools

| Tool | Aufgabe |
|---|---|
| `deja_recall` | Frühere Sitzungen nach Stichwort oder Fehler durchsuchen |
| `deja_session` | Den Kontext rund um einen Treffer lesen |
| `deja_blame` | Ermitteln, welche Sitzung eine Datei oder `path:line` verfasst hat |
| `deja_fix` | Befehl finden, der denselben Fehler früher behoben hat |
| `deja_how` | Verwendete Projekt-Flags und Aufrufe nachschlagen |
| `deja_remember` | Eine beschlossene Festlegung dauerhaft merken |

### Vier automatische Helfer

- **Start-Hinweis**: kompakter Toast in 1.x bzw. Systemkontext in 2.x (z. B. `deja-vu — from Codex CLI, 4 months ago: "…"`).
- **20 Minuten Abklingzeit & Subagenten-Filter**: verhindert Mehrfach-Einblendungen im gleichen Ordner und schont Subagenten.
- **Befehlsfehler & Dateihinweise**: ergänzt bei Fehlern frühere Fixes sowie frühere Festlegungen zu Dateien.
- **Vor Komprimierung**: sichert Aufgabe, Beschlüsse und offene Befehle beim Compact-Schritt.

## Optionen

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(In 2.x unter `"plugins"`.)

- `autoRecall`: automatische Injektion ein-/ausschalten (Standard `true`).
- `tools`: sechs Werkzeuge bereitstellen (Standard `true`).
- `bin`: eigener Pfad zur `deja`-Binary.

Mit `deja install --auto` lassen sich MCP (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) und automatischer Kontext für alle 50 Werkzeuge einrichten (siehe [Agentenwechsel ohne Kontextverlust](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Welche Binary genutzt wird

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> mitgeliefertes npm-Binary.

## Datenschutz

Der Index liegt in `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` konfigurierbar); `sk-*`, `ghp_*`, `AKIA*` und `Bearer` werden automatisch entfernt. Solange `deja embed` nicht manuell aktiviert wird, arbeitet alles offline.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 50 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## ## Lizenz

MIT
