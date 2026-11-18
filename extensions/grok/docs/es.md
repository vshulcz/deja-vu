# deja para Grok Build

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S · **13.6 s** para indexar 19,195 sesiones · **la mitad de tokens** que agentmemory) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Estrella en GitHub](https://github.com/vshulcz/deja-vu)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memoria local persistente procedente del historial de tus sesiones en Claude Code, Codex, Cursor, opencode, Zed y otros 44 agentes. Funciona localmente mediante el binario único en Go `deja` (`DEJA_OFFLINE=1` desactiva el chequeo diario).

## Instalación

Instala primero el binario `deja` (el plugin no empaqueta binarios nativos):

```sh
brew install deja-vu
# o:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Después instala el plugin desde el marketplace de este repositorio ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

También puedes usar `deja install --auto` para escribir `[mcp_servers.deja]` en `~/.grok/config.toml` y `~/.grok/hooks/deja.json`.

## Qué incluye

- **Herramienta MCP `deja`** con 8 modos (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **Skill `deja-history`** y comando **`/deja:recall <query>`**
- **5 hooks (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (sobre `Bash` en Grok 1.0.41), `PreCompact` y `SessionEnd`

## Sin conflicto si ya ejecutaste el instalador

Si `~/.grok/config.toml` ya define `[mcp_servers.deja]` o `~/.grok/hooks/deja.json` ya contiene hooks de deja, el plugin se detiene automáticamente para evitar duplicados. Usa `deja uninstall grok` si deseas quitar la configuración manual.

## Qué binario se usa

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## Retirarse sin parecer roto

Usa `${GROK_PLUGIN_ROOT}` y `.mcp.json`. En Grok Build 1.0.5, sin `deja` instalado `grok mcp doctor` y `grok inspect` mostrarán `handshake failed: connection closed`; basta instalar `deja` para resolverlo.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**la mitad de tokens** que agentmemory en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 49 agentes de programación (el siguiente de 7: 72 s) |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## ## Licencia

MIT

¿Te resultó útil? [Dale una estrella a deja-vu en GitHub](https://github.com/vshulcz/deja-vu).
