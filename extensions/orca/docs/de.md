# deja-vu für Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | Deutsch | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Orca-v2-Plugin (`trusted-worker`) für `deja` — bringt Session-Gedächtnis über 49 KI-Coding-Tools hinweg (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca und mehr), einschließlich Sessions vor der Installation von `deja`.

![deja-vu Demo](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Installation

Voraussetzung: [`deja`](https://github.com/vshulcz/deja-vu#install) im `PATH` (oder unter `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin` bzw. `%LOCALAPPDATA%\Programs\deja`).

Installiere das Plugin-Verzeichnis `extensions/orca` in Orca (`>=1.4.248`).

Für natives MCP, Skill-Dateien, `SessionStart`, `PreCompact`, `SessionEnd` sowie Fehler-Fix-Hinweise direkt in Orca zusätzlich ausführen:

```sh
deja install orca-auto
```

(`deja install orca` installiert MCP, Skill und `/deja` ohne Lifecycle-Hooks; `deja install orca-auto` oder `deja install --auto` ergänzt alle Hooks.)

Wenn `deja install orca-auto` bereits den nativen `UserPromptSubmit`-Hook in Orcas `settings.json` eingetragen hat, tritt dieser Worker automatisch zurück, damit `<deja-recall>`-Blöcke nicht doppelt injiziert werden.

## Funktionen des Plugins

- **Hooks**
  - `agent.prompt.before`: ermittelt den aktiven Workspace-Pfad und injiziert passenden `<deja-recall>`-Kontext innerhalb von Orcas 2-s-Budget (`1.3s` Timeout, fail-open).
  - `agent.tool.before`: hängt `<deja-recall>` beim Starten von `Agent`- oder `Task`-Subagents ausschließlich an `input.prompt` an (verändert nie `Bash`, `Read`, `Edit` oder andere Tool-Eingaben).
  - `agent.tool.after`: in Orcas Host-API nur beobachtend (`next` wird ignoriert, Orca wartet nicht); `fix`-Ausgaben bei fehlgeschlagenen Befehlen liefert daher `deja install orca-auto`.
- **Befehle**
  - `deja.orient` (`deja: Orient on workspace`): führt `deja hook-context --plain` für den aktuellen Workspace aus und zeigt eine Zusammenfassung.
  - `deja.recall` (`deja: Recall past sessions`): führt `deja search --limit 5 -- <query>` aus (bzw. `deja last -n 5` ohne Suchbegriff) und zeigt die Treffer an.

## Benchmarks

| Recall | Tokens | Indexierung & Suche |
|---|---|---|
| **97.2% R@5** auf LongMemEval-S (88.9% hit@1 über 470 Fragen; MemPalace veröffentlicht 96.6%, agentmemory 95.2%) | **58% weniger Tokens** (**halb so viele Tokens** wie agentmemory bei wiederholten Repo-Aufgaben) | **13.6 s** für 19,195 Sessions / **0.7 ms** Abfragezeit über 49 Coding-Agents hinweg (Nächstbester von 7: 72 s) |

## Repository-Gedächtnis & Agenten-Übergabe

Teile geprüfte Projektentscheidungen und Sackgassen per Git in `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Einträge werden beim Schreiben bereinigt, mit `merge=union` nach Content-Hash sortiert abgelegt und laden für jeden nach einem Clone automatisch (`{"activations":{"auto":{"repo":false}}}` in der Trust-Policy deaktiviert Hook-Injektionen). [Mehr](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Werkzeugwechsel mitten in der Aufgabe? Mit `deja handoff --to <agent>` (oder `handoff` über MCP) übergibst du Ziel, Entscheidungen, geänderte Dateien, bestandene Checks und offene Punkte direkt an den nächsten Coding-Agent.

## Lizenz

[MIT](../LICENSE)

Hilfreich? [Gib deja-vu einen Stern auf GitHub](https://github.com/vshulcz/deja-vu).
