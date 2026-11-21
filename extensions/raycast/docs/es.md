# deja-vu para Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Busca y reanuda sesiones de agentes de programación entre Claude Code, Codex, Cursor, OpenCode, Gemini CLI y 47 harnesses más directamente desde Raycast.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Requisitos previos

Instala [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Comandos

- **Search Sessions**: búsqueda de texto completo en cada sesión de agente en tu Mac (`deja search --json --limit 25 -- <query>`), separando coincidencias estrictas de resultados más cercanos por relevancia.
- **Recent Sessions**: explora las 100 sesiones más recientes de todos los agentes instalados (`deja last 100 --json`) con vista previa de metadatos (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Acciones

- **Show Session**: lee la conversación redactada dentro de Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: ejecuta el comando nativo para reanudar la sesión en `Terminal.app` (`deja resume <id>`).
- **Copy Resume Command**, **Copy Session ID** y **Open Project Folder**.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 52 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Desarrollo local

```sh
cd extensions/raycast && npm install && npx ray develop
```

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
