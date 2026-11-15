# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S · **13.6 s** für 19,195 Sessions · **halb so viele Tokens** wie agentmemory) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Stern auf GitHub](https://github.com/vshulcz/deja-vu)**

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

`session-query`-Plugin für DeepSeek Harness (`dsh`) und DSH Desktop (`anywhere-labs/dsh-desktop`). Durchsucht neben DSH-Gesprächen lokal auch die Protokolle von 47 weiteren Coding-Agenten auf demselben Rechner (Claude Code, Codex, Cursor, Gemini, Hermes u. a.) über die einzelne Go-Binary `deja` und deren BM25-Index (`DEJA_OFFLINE=1` deaktiviert die tägliche Prüfung).

## Installation

Beim Hinzufügen stets `--profile` angeben:

```sh
dsh plugin --profile web add dsh-deja
# oder direkt aus dem Repository:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

Fehlt `deja` noch auf dem System, `DEJA_BIN` setzen oder alle 48 Agenten samt `$DSH_HOME/plugins/deja/` einrichten:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## Was hinzugefügt wird

### Sechs Agenten-Werkzeuge

| Tool | Aufgabe |
|---|---|
| `deja_recall` | Frühere Sitzungen nach Fehlermeldung, Thema oder Flag durchsuchen |
| `deja_session` | Vollständige Gesprächszüge rund um einen Treffer lesen |
| `deja_blame` | Ermitteln, welche Sitzung eine Datei oder `path:line` bearbeitet hat |
| `deja_fix` | Den Befehl abrufen, der denselben Fehler früher behoben hat |
| `deja_how` | Verwendete Build-/Test-Flags im Projekt nachschlagen |
| `deja_remember` | Eine getroffene Entscheidung festhalten |

- **`/deja <...>`**: Direktsuche über das Eingabefeld.
- **Laufzeiten**: läuft mit `deja install dsh-auto`, `dsh web`, `tui`, `headless` und `sdk` (`autoRecall: false` in YAML deaktiviert die automatische Injektion).

## Suchsteuerung & Tastenkürzel

Konfigurierbar unter `Settings → Deja`:

- **Mod+F** (oder **Mod+Alt+F** / macOS **Cmd+Option+F**): öffnet das Suchfeld; zum Eingrenzen `/deja --session <bare session UUID> -- <your draft>` nutzen.
- **Mod+Shift+K** (macOS **Cmd+Shift+K**): globale Suche über alle Agenten hinweg.
- **Mod+K**: öffnet das Panel inklusive Schalter **DSH conversations only** (bis zu 20 Treffer).
- **Linux-Browser-Alternativen**: **`Mod+/`**, **`Mod+Shift+,`** und **`Mod+Shift+.`** (`shortcuts.open`).

## Tastenkürzel anpassen

In `Settings → Deja` bei **`session.search`** über **Change shortcut** anpassen, per **Restore default** zurücksetzen oder via **Remove** löschen. Im Hintergrund stellt das **dsh-deja package host plugin** `/api/deja-search` bereit; liefert `/api/deja-search?probe=1` ein `{"ok": true, "plugin": "dsh-deja"}`, antwortet `deja search` korrekt.

## Technische Einbindung

- **System-Kontext**: übergibt `additionalContext` und `systemMessage` via `ctx.systemPrompt.context` und `agent/pre-step` (`dsh 0.1.1-rc.2`+).
- **Endpunkte & Grenzen**: `/api/deja-notes` (`--plain`) versorgt `client.js`; `GET /api/deja-search` ruft `deja search --harness deepseek --json --limit 20 -- <query>` mit 20 Sekunden Timeout, 2 MiB Output-Limit und 500 Zeichen Query-Limit auf.
- **Seitenleiste**: bindet über `data-slot="sidebar.workspaces"`, `aria-expanded` und `_listArea` ein (DSH `0.2.1-rc.5`).

## Suchkomponenten testen

```sh
node --test extensions/dsh/test/*.test.js
```

Eine Browser-Testdatei ohne Build-Schritt liegt unter `extensions/dsh/test/search-browser.html` (React 18 CDN).

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen; MemPalace veröffentlicht 96.6%, agentmemory 95.2%) | **58% weniger Tokens** (**halb so viele Tokens** wie agentmemory bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 48 Coding-Agents hinweg (Nächstbester von 7: 72 s) |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Was es bewusst nicht tut

Der Index bleibt lokal unter `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), ohne manuelles `deja embed` gibt es keine externen API-Aufrufe, `sk-*`, `ghp_*`, `AKIA*` und `Bearer` werden beim Indexieren entfernt und Originaldateien nie angetastet.

## Lizenz

Teil von [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Hilfreich? [Gib deja-vu einen Stern auf GitHub](https://github.com/vshulcz/deja-vu).
