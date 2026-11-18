# `@vshulcz/magpie-deja` — Magpie-Gateway-Hook für deja-vu

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S · **13.6 s** für 19,195 Sessions · **halb so viele Tokens** wie agentmemory) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> Reicht Anfragen über [Magpie](https://github.com/yetone/magpie) automatisch mit lokalem Sitzungswissen aus 47 weiteren Coding-Agenten (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes u. a.) an.

Unterstützt OpenAI (`chat`, `responses`), Anthropic (`anthropic`) und Gemini (`gemini`) über die lokale Go-Binary `deja` und deren BM25-Index ohne zusätzliches LLM (`DEJA_OFFLINE=1` deaktiviert den täglichen Release-Check).

## Installation

```sh
magpie plugin add @vshulcz/magpie-deja
```

Voraussetzung sind die Binary `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` oder `install.sh`) sowie ein via `deja index` aufgebauter Index. Für volle MCP-Werkzeuge (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) in allen 48 Agenten:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

Enthält eine Anfrage bereits `<deja-memory>` oder `<deja-recall>`, verzichtet das Plugin auf eine erneute Einfügung.

## Funktionsweise & Datengrenze

1. **`onRequest` startet `deja hook-prompt --plain`**: fragt `~/.cache/deja/index.db` (per `DEJA_INDEX_DIR` änderbar) innerhalb von `800ms` ab und fügt `<deja-memory>` ein. **Hinweis zu ausgehenden Anfragen**: Der eingefügte Verlauf wird mit dem Request an den Upstream-LLM-Anbieter übertragen.
2. **Strikter Verzeichnisbezug (`ctx.cwd`)**: Mit `allowUnscoped: false` fällt `deja hook-prompt` niemals auf `process.cwd()` des Gateways zurück.
3. **Standardmäßig nur lokal**: `recallRemote: false` beschränkt Abrufe auf `ctx.origin === "local"` und `ctx.origin === "magpie-key"`.
4. **Zweistufige Schwärzung**: Bereinigung beim Indexieren sowie vor dem Versand mittels `maskSecrets` aus `deja.hooks.mjs` (`sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, URL-Zugangsdaten, `api_key=...`).
5. **Schleifen- und Präfix-Cache-Schutz**: Ignoriert bereits markierte Anfragen (`<deja-memory`, `<deja-recall>`, `[deja —`) und erhält den Prompt-Prefix-Cache.

## Optionen

| Option | Standard | Bedeutung |
|---|---|---|
| `requestTimeoutMs` | `800` | Zeitlimit pro `deja`-Aufruf (`50`–`5000` ms) |
| `maxBytes` | `4000` | Obergrenze des eingefügten Blocks (`256`–`32768` Bytes) |
| `recallRemote` | `false` | Abruf auch bei entfernten Request-Quellen erlauben |
| `allowUnscoped` | `false` | Globale Suche ohne `ctx.cwd` zulassen |
| `cwd` | `""` | Projektverzeichnis explizit setzen |
| `skipAgents` | `[]` | Ausgeschlossene Agenten (z. B. `["claude-code"]`) |
| `bin` | `""` | Pfad zur `deja`-Binary |

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen; MemPalace veröffentlicht 96.6%, agentmemory 95.2%) | **58% weniger Tokens** (**halb so viele Tokens** wie agentmemory bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 48 Coding-Agents hinweg (Nächstbester von 7: 72 s) |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Hilfreich? [Gib deja-vu einen Stern auf GitHub](https://github.com/vshulcz/deja-vu).
