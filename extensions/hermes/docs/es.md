# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Un proveedor de memoria para Hermes que responde a partir de las sesiones de código ya guardadas en este equipo: Claude Code, Codex, Cursor, el propio Hermes y el resto de agentes que deja lee, incluidos los meses anteriores a instalar herramienta alguna. Sin modelo extra, sin clave de API y sin servidor: [deja](https://github.com/vshulcz/deja-vu), un único binario en Go, indexa el historial en local.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalación

`deja` debe estar primero en el `PATH`:

```bash
brew install deja-vu        # o: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # conecta Hermes y todos los agentes compatibles en esta máquina
hermes plugins install deja-memory   # o desde GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

o bien selecciona `deja-memory` en `hermes memory setup`, que comprueba el binario antes de activar el proveedor.

## Qué hace

- **Antes del primer turno de una sesión**: entrega a Hermes el mismo resumen de proyecto que los hooks de deja pasan a Claude Code y Codex (el trabajo reciente y lo acordado). Después, solo interviene cuando la consulta coincide con algo anterior; en la mayoría de turnos no añade nada.
- **Ves lo que trajo**: las líneas que deja muestra en Claude Code (el recibo del agente de origen, lo recuperado al arrancar la sesión y las notas semanales o de compilación) viajan en la línea de memoria de Hermes, `🧠 deja · …`, en la CLI interactiva y en `hermes chat -q`. El texto del modelo no cambia. Hermes 0.17 carece de línea de memoria, `hermes -z` y `hermes chat -Q` no la imprimen, la TUI solo la muestra brevemente en la barra de estado y un chat de gateway puede ser compartido, por lo que ahí deja guarda sus notas para la próxima sesión que sí las muestre.
- **Sin inyección automática en tareas `cron`**: cuando Hermes indica plataforma `cron`, el proveedor no añade nada por su cuenta, aunque las herramientas de deja siguen respondiendo si la tarea las invoca.
- **Tres herramientas**: `deja_recall` busca sesiones pasadas, `deja_fix` indica qué se ejecutó tras un error la última vez y `deja_blame` enumera las sesiones que editaron un archivo.
- **Prioridad para notas**: cualquier entrada que Hermes añada a `MEMORY.md` o `USER.md` queda también como nota en deja y se ordena por encima de las sesiones cercanas.
- **Compactación limpia**: conserva en qué punto estaba la sesión para el turno siguiente tras compactar, y limpia su marca activa al cerrar la sesión.

Hay algo que un proveedor de memoria no puede hacer solo: colocar la solución anterior junto a un comando recién fallido. Eso depende de `transform_tool_result`, un hook de plugin donde Hermes pasa a los proveedores de memoria un contexto cuyo `register_hook` no hace nada (`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` instala el plugin de hooks encargado de ello.

Nunca duplica los turnos de Hermes: Hermes ya los guarda y deja lee ese almacenamiento en su siguiente actualización.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 50 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Mismo código que `deja install`

`deja install hermes-auto` escribe este proveedor en `~/.hermes/plugins/deja-memory` junto con el servidor MCP y un plugin de hook `pre_llm_call`. Los archivos aquí se generan desde la misma fuente (`cmd/deja/install_hermes_memory.go`); la única diferencia es que esta copia busca `deja` en `PATH` en lugar de fijar la ruta. Si divergen, falla el test `TestHermesExtensionIsTheProviderInstallWrites`.

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
