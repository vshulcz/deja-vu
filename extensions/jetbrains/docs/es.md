# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Búsqueda local de sesiones entre agentes, AI Line Blame y relevo de contexto para IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm y Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) indexa en SQLite FTS5 local las transcripciones de sesión que Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf y otros 39 agentes de código escriben en disco, con redacción automática de secretos y sin servicios en la nube.

**[Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Funcionalidades

- **Widget en la barra de estado**: muestra en tiempo real las estadísticas de `deja statusline` para el proyecto activo a la derecha de la barra de estado del IDE. Al hacer clic se abre el menú emergente de acciones de `deja-vu`.
- **Buscar sesiones de IA (`Tools → deja-vu → Search AI Sessions...`)**: consulta sesiones locales en los 50 agentes compatibles (`deja search --json`) y abre la transcripción elegida en una pestaña Markdown de solo lectura.
- **Sesiones recientes (`Tools → deja-vu → Recent AI Sessions`)**: enumera las 50 sesiones más recientes del equipo (`deja last 50 --json`) con filtrado rápido por texto.
- **AI Blame en la línea actual (menú contextual / `Tools → deja-vu → AI Blame Current Line`)**: ejecuta `deja blame <file>:<line> --json` (con respaldo en `<file>`) para abrir la sesión de IA que escribió o modificó la línea bajo el cursor.
- **Copiar relevo entre agentes (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: ejecuta `deja handoff` en la raíz del proyecto, copia el resumen al portapapeles y lo abre en una pestaña Markdown de solo lectura.

## Instalación

1. Instala el binario CLI `deja` y conecta tus agentes locales:

```sh
brew install deja-vu && deja install --auto
# o sin Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Instala el plugin de JetBrains ([`com.github.vshulcz.deja-vu` en JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)):

- **Desde JetBrains Marketplace**: [instala directamente desde plugins.jetbrains.com](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) o abre **Settings → Plugins → Marketplace** y busca `deja-vu`.
- **Desde disco**: abre **Settings → Plugins → ⚙️ → Install Plugin from Disk...** y selecciona `deja-jetbrains.zip` desde [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **Desde la CLI**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Sustituye `idea` por `goland`, `pycharm`, `webstorm`, `rustrover` o `clion` según tu IDE.)*

## Compilación desde fuentes

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

El paquete instalable se guarda en `build/distributions/`.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 50 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
