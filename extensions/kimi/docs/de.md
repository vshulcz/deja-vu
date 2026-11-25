# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indiziert die Session-Dateien auf deiner Festplatte — von Claude Code, Codex, Cursor, opencode und 49 weiteren Agents — und antwortet daraus. Dieses Plugin bringt den Index in Kimi Code: Relevanter Rückruf landet mit deinem Prompt, und der Agent kann selbst im Verlauf suchen.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Diese Form pinnt das neueste Release und merkt sich die Quelle für Kimis Update-Prüfung. Außerdem gibt es `kimi-deja.zip` als Release-Asset (~10 KB statt des gesamten Repos) für Marketplace-Einträge oder schlanke Installationen.

Installiere vorher das Binary (und verbinde optional direkt alle weiteren Agents auf diesem Rechner):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Plugins werden aus der verwalteten Kopie geladen — führe danach `/reload` oder `/new` aus.

## Updates

Kimi benachrichtigt nur bei Plugins aus dem eigenen Marketplace. Bei einer Repository-Installation holt ein erneutes `/plugins install` das aktuelle Release, und `deja doctor` weist darauf hin, wenn die installierte Kopie hinter der dieses `deja` liegt:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## Enthaltene Funktionen

- **Die Hooks aus `deja install kimi-auto`**: Beim ersten Prompt einer Session die Projektbeschlüsse (`deja hook-context --once`). Bei jedem Prompt gezielter Rückruf zum Anliegen (`deja hook-prompt`), den Kimi an den Zug anhängt — bei keinem Treffer bleibt alles leer. Vor dem Komprimieren liest deja die betroffenen Züge ein, damit der nächste Prompt am laufenden Stand anknüpft (`deja hook-precompact`). Bei `SessionEnd` wird der Live-Status entfernt (`deja hook-session-end`).
- **Tools**: Bindet `deja mcp` als MCP-Server mit dem einen Tool `deja` und den Modi `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` oder `handoff` ein.
- **`/deja:recall <query>`** für direkte Verlaufsabfragen.
- **Der Skill `deja-history`**, beim Start geladen, damit der Agent vor erneutem Debugging erst nachschlägt.

## Welches Binary läuft

In dieser Reihenfolge: `DEJA_BIN`, ein selbst installiertes `deja` (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), dann `deja` im `PATH`. Ein eigenes `deja update` oder `brew upgrade deja-vu` gewinnt immer.

## Wenn du außerdem `deja install kimi` ausgeführt hast

Die CLI trägt denselben MCP-Server in `~/.kimi-code/mcp.json` und denselben Hook in `config.toml` ein. Kimi startet Plugin-Server unter eigenem Namespace (`plugin-deja:deja`), sodass sonst alles doppelt liefe.

Dieses Plugin prüft beim Hook-Aufruf und Server-Start auf die CLI-Einrichtung und tritt zurück, sobald sie vorhanden ist — weil `deja install` diese aktuell hält. `deja uninstall kimi` gibt die Zuständigkeit ohne Neuinstallation ans Plugin zurück.

`deja doctor` meldet für kimi `plugin`, wenn das Plugin die Anbindung trägt.

Zwei Dinge erkennt es nicht: einen manuell unter anderem Namen als `deja` eingetragenen MCP-Server und einen eigenen Hook ohne den Markerkommentar des Installers.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 53 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

MIT

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
