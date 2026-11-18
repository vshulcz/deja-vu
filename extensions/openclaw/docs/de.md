# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S · **13.6 s** für 19,195 Sessions · **halb so viele Tokens** wie agentmemory) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Stern auf GitHub](https://github.com/vshulcz/deja-vu)**

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Bindet Sitzungsprotokolle von 48 weiteren Coding-Agenten (Claude Code, Codex, Cursor, Gemini, Zed u. a.) direkt in [OpenClaw](https://openclaw.ai) ein. Läuft komplett lokal mit der Go-Binary `deja` und BM25 ohne LLM (`DEJA_OFFLINE=1` deaktiviert die tägliche Prüfung).

## Installation

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Optional vorab die System-Binary installieren:

```sh
brew install deja-vu
```

Wurde bereits `deja install openclaw-auto` oder `deja install --auto` ausgeführt, hält sich dieses Paket automatisch zurück.

## Funktionsweise

- **`agent:bootstrap` (inkl. `openclaw agent --local`)**: lädt zum Sitzungsstart frühere Projektfestlegungen.
- **`before_prompt_build` (ab OpenClaw `2026.8.1` mit `hooks.allowConversationAccess: true`)**: ergänzt zu jedem Prompt passende historische Treffer.
- **`before_compaction`**: rettet Aufgabe, Ergebnisse und Befehlsstände vor der Zusammenfassung.
- **`session_end`**: aktualisiert den lokalen Suchindex.
- **Tool-Ergebnis-Middleware**: liefert bei Befehlsfehlern frühere Fixes sowie Kontext zu bearbeiteten Dateien.
- **Tools `deja_recall` / `deja_fix` / `deja_blame`**: gezielter Abruf von Verlauf, Fehlerlösungen und Datei-Ursprüngen.
- **`/deja <query>`**: Direktabfrage über alle Werkzeuge hinweg.

### Quellenhinweis in Direktnachrichten (Receipt)

Unter passenden DM-Antworten hängt `reply_payload_sending` eine kursive Quellenzeile an:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

Das geschieht ausschließlich, wenn alle vier Bedingungen erfüllt sind:

1. Direktnachricht ohne Gruppe (`isGroup: false` / `direct`).
2. Absender durch `commands.ownerAllowFrom` / `allowFrom` autorisiert.
3. Block-Streaming ausgeschaltet (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. Finale Textantwort im Kanal `direct`.

Mit `"receipt": false` lässt sich dieser Hinweis abschalten.

## Konfiguration

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

Oder per CLI:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Binary-Auflösung

`bin` -> `DEJA_BIN` -> `PATH` -> Standardpfade -> Paketkopie aus `@vshulcz/deja-vu`.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen; MemPalace veröffentlicht 96.6%, agentmemory 95.2%) | **58% weniger Tokens** (**halb so viele Tokens** wie agentmemory bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 49 Coding-Agents hinweg (Nächstbester von 7: 72 s) |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Datenschutz

Der Index liegt in `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), wobei `sk-*`, `ghp_*`, `AKIA*` und `Bearer` beim Indexieren entfernt werden. Ohne manuelles `deja embed` erfolgt kein Netzversand. Alle 49 unterstützten Agenten lassen sich per `deja install --auto` einbinden.

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Hilfreich? [Gib deja-vu einen Stern auf GitHub](https://github.com/vshulcz/deja-vu).
