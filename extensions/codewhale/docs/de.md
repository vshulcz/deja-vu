# deja-vu für CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Claude-kompatibles Plugin-Paket für CodeWhale — stellt den `deja`-MCP-Server (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), den `deja-history`-Skill und `/deja` über Sessions aus 49 KI-Coding-Tools bereit.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Voraussetzung: [`deja`](https://github.com/vshulcz/deja-vu#install) im `PATH`.

Installiere dieses Bundle in CodeWhale per `/plugin install` aus dem Unterverzeichnis `extensions/codewhale`.

Da CodeWhales Claude-Kompatibilitätslader MCP-Server, Skills und Slash-Commands importiert, aber `hooks` und `lspServers` ignoriert, führe `deja install codewhale-auto` (oder `deja install --auto`) aus, wenn auch native CodeWhale-Lifecycle-Hooks (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) in `~/.codewhale/config.toml` eingetragen werden sollen (`deja install codewhale` richtet nur MCP und `tools.always_load` ein):

```sh
deja install codewhale-auto
```

## Paketinhalt

- `.claude-plugin/plugin.json`: Plugin-Metadaten (`name: deja-vu`)
- `.mcp.json`: Stdio-Server-Eintrag `deja mcp`
- `skills/deja-history/SKILL.md`: Skill-Anweisungen für die `deja`-Modi
- `commands/deja.md`: Slash-Command `/deja`

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen) | **58% weniger Tokens** (bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 49 Coding-Agents hinweg |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

[MIT](../LICENSE)
