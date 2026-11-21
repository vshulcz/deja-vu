# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Lokale agentenübergreifende Session-Suche, KI-Zeilen-Blame und Handoff für IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm und Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) indiziert die Session-Protokolle von Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf und 43 weiteren Coding-Agents in einem lokalen BM25-Index — mit automatischer Secret-Bereinigung und ohne Cloud-Dienst.

**[Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Funktionen

- **Statusleisten-Widget**: Zeigt Live-Statistiken von `deja statusline` für das aktive Projekt rechts in der IDE-Statusleiste. Ein Klick öffnet das `deja-vu`-Aktionsmenü.
- **KI-Sessions durchsuchen (`Tools → deja-vu → Search AI Sessions...`)**: Durchsucht lokale Sessions aller 52 unterstützten Coding-Agents (`deja search --json`) und öffnet das gewählte Protokoll in einem schreibgeschützten Markdown-Tab.
- **Letzte KI-Sessions (`Tools → deja-vu → Recent AI Sessions`)**: Listet die 50 jüngsten Sessions auf dem Rechner (`deja last 50 --json`) mit Schnellsuche auf.
- **AI Blame für aktuelle Zeile (Kontextmenü / `Tools → deja-vu → AI Blame Current Line`)**: Führt `deja blame <file>:<line> --json` (mit Fallback auf `<file>`) aus und öffnet die KI-Session, die die Zeile unter dem Cursor geschrieben oder geändert hat.
- **Agenten-Handoff kopieren (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: Führt `deja handoff` im Projektstamm aus, kopiert die Zusammenfassung in die Zwischenablage und öffnet sie in einem schreibgeschützten Markdown-Tab.

## Installation

1. Installiere die `deja`-CLI und richte deine lokalen Coding-Agents ein:

```sh
brew install deja-vu && deja install --auto
# oder ohne Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Installiere das JetBrains-Plugin ([`com.github.vshulcz.deja-vu` im JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)):

- **Über den JetBrains Marketplace**: [Direkt von plugins.jetbrains.com installieren](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) oder **Settings → Plugins → Marketplace** öffnen und nach `deja-vu` suchen.
- **Von der Festplatte**: **Settings → Plugins → ⚙️ → Install Plugin from Disk...** mit `deja-jetbrains.zip` aus [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **Per CLI**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Ersetze `idea` je nach IDE durch `goland`, `pycharm`, `webstorm`, `rustrover` oder `clion`.)*

## Aus dem Quellcode bauen

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

Das installierbare Archiv liegt anschließend unter `build/distributions/`.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 52 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
