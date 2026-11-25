# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indexa los archivos de sesión que los agentes de código ya guardan en disco —Claude Code, Codex, Cursor, opencode y 49 más— y responde a partir de ellos. Esta extensión expone ese índice en el panel de agente de Zed como servidor de contexto, para que un hilo busque trabajo anterior incluso previo a instalar deja.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Qué recibe el agente

Una sola herramienta, `deja`, invocada con un `mode`:

| Mode | Qué responde |
|---|---|
| `recall` | Sesiones que coinciden con un error, nombre de función, ruta o flag. |
| `context` | El resumen completo de una sesión pasada cuando importa el razonamiento. |
| `blame` | Las sesiones que trataron un archivo antes de editarlo o borrarlo. |
| `fix` | Qué ejecutó esta máquina tras ese mismo error anteriormente. |
| `how` | El comando real de compilación, prueba o despliegue ejecutado aquí. |
| `orient` | Los comandos ejecutados en este proyecto y los archivos tocados. |
| `remember` | Guarda una decisión duradera para recuperarla más adelante. |
| `handoff` | El estado de otra sesión para retomar el trabajo donde se quedó. |

## Instalación

Instala la extensión desde la lista de extensiones de Zed. En el primer uso pide a Zed ejecutar `deja --version` y, si funciona, utiliza el `deja` presente en el `PATH` de Zed. De lo contrario descarga el binario de la release en su propio directorio. Como las extensiones corren aisladas en sandbox y no pueden inspeccionar rutas habituales, si `deja` no está en el `PATH` con el que arrancó Zed debes indicarlo así:

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` también configura Zed escribiendo el servidor directamente en `settings.json`, la vía más rápida si ya tienes la CLI. Ambos usan el mismo ID (`deja-context-server`) y Zed identifica los servidores por ID, de modo que en cualquier orden queda exactamente una entrada. `deja uninstall zed` elimina lo que añadió la CLI.

El indexado y la búsqueda son locales: sin llamadas de red y con redacción de credenciales al construir el índice.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 53 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

Licencia MIT, igual que deja.

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
