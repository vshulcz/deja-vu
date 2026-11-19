# deja-vu für VS Code, Cursor, Windsurf & Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**Deine Coding-Agents debuggen nicht noch einmal, was du längst behoben hast.**

[deja-vu](https://github.com/vshulcz/deja-vu) indiziert die Session-Transkripte, die Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code und 41 weitere Agents lokal ablegen — in einem Go-Binary, ohne LLM und ohne Hintergrund-Daemon.

**[★ Stern auf GitHub](https://github.com/vshulcz/deja-vu)** · **[Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Funktionsumfang

Zeigt `deja statusline` live in der Statusleiste der IDE an (indizierte Sessions aller Agents, heute ausgelieferte Recalls und Workspace-Verlauf). Ist `deja` noch nicht installiert, startet ein Klick auf den Eintrag `npx -y @vshulcz/deja-vu install --auto` im Terminal und bindet alle erkannten Agents an.

## Schnellinstallation

```sh
brew install deja-vu && deja install --auto
# oder ohne Homebrew:
npx -y @vshulcz/deja-vu install --auto
```

Statusleisten-Erweiterung per CLI installieren:

```sh
code --install-extension vshulcz.deja-status
```

## Gemessene Wirkung

| Benchmark | Ergebnis |
|---|---|
| LongMemEval-S Treffergenauigkeit (470 bereinigte Fragen) | **97.2% R@5**, **88.9% hit@1** (MemPalace 96.6%, agentmemory 95.2%) |
| Token-Kosten bei wiederkehrender Repo-Arbeit | **58% weniger Tokens** — halb so viele wie agentmemory (`52,815` vs. `103,443` kalt, 11 Paare) |
| Latenz der In-Process-Suche | **0.7–0.8 ms** Median über 49 Coding-Agents hinweg |
| Erstindexierung über 19,195 echte Sessions | **13.6 s**, ohne API-Schlüssel (Nächstbester von 7: 72 s) |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Hilfreich? [Gib deja-vu einen Stern auf GitHub](https://github.com/vshulcz/deja-vu).
