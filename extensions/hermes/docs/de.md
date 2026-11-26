# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Ein Hermes-Memory-Provider, der aus den bereits auf diesem Rechner liegenden Coding-Sessions antwortet: Claude Code, Codex, Cursor, Hermes selbst und alle übrigen von deja gelesenen Agents — einschließlich der Monate vor jeder Installation. Kein LLM, kein API-Schlüssel, kein Daemon; indiziert wird lokal von [deja](https://github.com/vshulcz/deja-vu), einem einzelnen Go-Binary.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

`deja` muss zunächst im `PATH` liegen:

```bash
brew install deja-vu        # oder: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # richtet Hermes und alle anderen unterstützten Agents auf diesem Rechner ein
hermes plugins install deja-memory   # oder von GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

oder wähle `deja-memory` in `hermes memory setup`, das vor der Aktivierung prüft, ob das Binary vorliegt.

## Was der Provider tut

- **Vor dem ersten Zug einer Session**: Übergibt Hermes denselben Projekt-Digest wie dejas Hooks für Claude Code und Codex — aktuelle Arbeit in diesem Projekt und bestehende Entscheidungen. Danach nur, wenn die aktuelle Anfrage zu Früherem passt; in den meisten Zügen wird nichts ergänzt.
- **Nachvollziehbar sichtbar**: Die Hinweise aus Claude Code (Quittung mit Quell-Agent, Start-Rückruf, Build- und Wochenhinweise) laufen in der interaktiven CLI und `hermes chat -q` über Hermes' Hinweiszeile `🧠 deja · …`. Der Modelltext bleibt unverändert. Hermes 0.17 besitzt keine Hinweiszeile, `hermes -z` und `hermes chat -Q` geben keine aus, die TUI blendet sie nur kurz in der Statuszeile ein und ein Gateway-Chat kann eine Gruppe mit Mitlesern sein — dort hebt deja die Hinweise für eine Session auf, die sie anzeigt.
- **Kein automatischer Rückruf bei `cron`**: Meldet Hermes die Plattform `cron`, fügt der Provider von sich aus nichts hinzu; ruft der Job die deja-Tools direkt auf, antworten sie normal.
- **Drei Tools**: `deja_recall` durchsucht frühere Sessions, `deja_fix` nennt die Lösung nach demselben Fehler beim letzten Mal und `deja_blame` listet Sessions auf, die eine Datei bearbeitet haben.
- **Notizen höher gewichten**: Einträge von Hermes in `MEMORY.md` oder `USER.md` bleiben ebenfalls als deja-Notiz erhalten und ranken über umliegenden Sessions.
- **Saubere Komprimierung**: Behält nach einer Komprimierung den aktuellen Arbeitsschritt für den nächsten Zug und entfernt am Session-Ende die aktive Markierung.

Eines kann ein Memory-Provider allein nicht leisten: die frühere Lösung direkt neben einen gerade gescheiterten Befehl stellen. Das läuft über `transform_tool_result`, einen Plugin-Hook, während Hermes Memory-Providern einen Kontext übergibt, dessen `register_hook` nichts tut (`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` legt das dafür zuständige Hook-Plugin an.

Er schreibt Hermes-Züge nirgendwo doppelt hin: Hermes speichert sie bereits selbst, und deja liest diesen Speicher bei der nächsten Aktualisierung.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 53 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Identischer Code zu `deja install`

`deja install hermes-auto` installiert diesen Provider unter `~/.hermes/plugins/deja-memory` zusammen mit dem MCP-Server und einem `pre_llm_call`-Hook-Plugin. Die Dateien hier stammen aus derselben Quelle (`cmd/deja/install_hermes_memory.go`); der einzige Unterschied besteht darin, dass diese Kopie `deja` im `PATH` sucht statt einen festen Pfad einzutragen. `TestHermesExtensionIsTheProviderInstallWrites` schlägt fehl, sobald beide auseinanderlaufen.

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
