# deja für Grok Build

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Teil von **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** auf LongMemEval-S) · [Dokumentation](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Live-Demo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Dauerhaftes lokales Gedächtnis aus Ihren bisherigen Sitzungen in Claude Code, Codex, Cursor, opencode, Zed und 44 weiteren Coding-Agenten. Alles läuft lokal über die einzelne Go-Binary `deja` (`DEJA_OFFLINE=1` deaktiviert den täglichen Release-Check).

## Installation

Installieren Sie zuerst die Binary `deja` (Plugins liefern keine nativen ausführbaren Dateien):

```sh
brew install deja-vu
# oder:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Danach den Marketplace dieses Repos einbinden ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

Alternativ richtet `deja install --auto` direkt `[mcp_servers.deja]` in `~/.grok/config.toml` sowie `~/.grok/hooks/deja.json` ein.

## Lieferumfang

- **MCP-Tool `deja`** mit 8 Modi (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **Skill `deja-history`** und **`/deja:recall <query>`**
- **5 Hooks (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (für `Bash` ab Grok 1.0.41), `PreCompact` und `SessionEnd`

## Doppelte Einrichtung ist unproblematisch

Steht `[mcp_servers.deja]` bereits in `~/.grok/config.toml` oder sind Hooks in `~/.grok/hooks/deja.json` eingetragen, hält sich das Plugin zurück. Zum Entfernen der CLI-Einträge dient `deja uninstall grok`.

## Binary-Auflösung

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## Sauberes Verhalten ohne Binary

Genutzt werden `${GROK_PLUGIN_ROOT}` und `.mcp.json`. Fehlt `deja` unter Grok Build 1.0.5, melden `grok mcp doctor` und `grok inspect` zunächst `handshake failed: connection closed`, bis `deja` installiert ist.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 49 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## ## Lizenz

MIT
