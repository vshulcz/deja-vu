# deja-vu para Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Plugin `trusted-worker` de Orca v2 para `deja`: aporta memoria de sesiones entre 49 herramientas de programación con IA (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca y más), incluidas sesiones grabadas antes de instalar `deja`.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalación

Requisitos: [`deja`](https://github.com/vshulcz/deja-vu#install) en `PATH` (o instalado en `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin` o `%LOCALAPPDATA%\Programs\deja`).

Instala el directorio de plugin `extensions/orca` en Orca (`>=1.4.248`).

Para contar con MCP nativo, archivos de skills, `SessionStart`, `PreCompact`, `SessionEnd` y sugerencias `fix` tras comandos fallidos dentro de Orca, ejecuta también:

```sh
deja install orca-auto
```

(`deja install orca` instala MCP, skill y `/deja` sin hooks de ciclo de vida; `deja install orca-auto` o `deja install --auto` añade el conjunto completo de hooks.)

Cuando `deja install orca-auto` ya escribió el hook nativo `UserPromptSubmit` en `settings.json` de Orca, este worker cede el paso automáticamente para no inyectar marcos `<deja-recall>` por duplicado.

## Qué aporta este plugin

- **Hooks**
  - `agent.prompt.before`: resuelve la ruta del espacio de trabajo activo e inyecta contexto relevante `<deja-recall>` en el prompt dentro del margen de 2 s de Orca (tiempo límite `1.3s`, fail-open).
  - `agent.tool.before`: al lanzar subagentes `Agent` o `Task`, añade contexto `<deja-recall>` solo a `input.prompt` (nunca altera las entradas de `Bash`, `Read`, `Edit` ni otras herramientas).
  - `agent.tool.after`: solo observador en la API host de Orca (se ignora `next` y Orca no espera), por lo que la entrega de `fix` en errores proviene de `deja install orca-auto`.
- **Comandos**
  - `deja.orient` (`deja: Orient on workspace`): ejecuta `deja hook-context --plain` sobre el área de trabajo activa y muestra un aviso resumen.
  - `deja.recall` (`deja: Recall past sessions`): ejecuta `deja search --limit 5 -- <query>` (o `deja last -n 5` si no se indica consulta) y muestra el resultado.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 49 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

[MIT](../LICENSE)
