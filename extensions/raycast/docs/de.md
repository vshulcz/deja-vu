# deja-vu für Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Durchsuche Coding-Agent-Sessions aus Claude Code, Codex, Cursor, OpenCode, Gemini CLI und 48 weiteren Tools direkt aus Raycast und setze sie dort fort.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Voraussetzungen

Installiere [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Befehle

- **Search Sessions**: Volltextsuche über jede Coding-Agent-Session auf deinem Mac (`deja search --json --limit 25 -- <query>`), getrennt nach exakten Treffern und ähnlichsten Relevanztreffern.
- **Recent Sessions**: Die 100 neuesten Sessions aller installierten Agents durchsuchen (`deja last 100 --json`) inklusive Metadaten-Vorschau (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Aktionen

- **Show Session**: Bereinigtes Transkript direkt in Raycast lesen (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: Nativen Resume-Befehl des jeweiligen Agents (`deja resume <id>`) in `Terminal.app` ausführen.
- **Copy Resume Command**, **Copy Session ID** und **Open Project Folder**.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 53 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lokale Entwicklung

```sh
cd extensions/raycast && npm install && npx ray develop
```

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
