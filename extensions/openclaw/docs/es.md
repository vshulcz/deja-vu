# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Conecta el historial en disco de otros 52 agentes de programación (Claude Code, Codex, Cursor, Gemini, Zed, etc.) dentro de [OpenClaw](https://openclaw.ai). Se apoya en el binario único en Go `deja` y su índice BM25 local sin LLM (`DEJA_OFFLINE=1` desactiva el aviso diario).

## Instalación

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Opcionalmente instala el binario en el sistema:

```sh
brew install deja-vu
```

Si ya ejecutaste `deja install openclaw-auto` o `deja install --auto`, este plugin cede para evitar inyectar contexto por duplicado.

## Qué hace

- **`agent:bootstrap` (compatible con `openclaw agent --local`)**: carga al abrir la sesión las decisiones previas del proyecto.
- **`before_prompt_build` (con OpenClaw `2026.8.1` y `hooks.allowConversationAccess: true`)**: busca contexto histórico relevante antes de responder.
- **`before_compaction`**: preserva la tarea actual, conclusiones y comandos ejecutados antes de compactar.
- **`session_end`**: actualiza el índice al cerrar la sesión.
- **Middleware de resultados de herramienta**: ante un fallo o cambio en un archivo clave, añade arreglos o decisiones pasadas.
- **Herramientas `deja_recall` / `deja_fix` / `deja_blame`**: permiten al agente consultar recuerdos, comandos de corrección y autoría por archivo.
- **`/deja <query>`**: búsqueda interactiva sobre todos los agentes indexados.

### Recibo discreto en mensajes directos

Cuando se cumplen las condiciones en un mensaje directo, `reply_payload_sending` añade una línea en cursiva al final de la respuesta:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

Solo se añade si se cumplen las cuatro condiciones:

1. Conversación directa sin grupo (`isGroup: false` / `direct`).
2. El remitente cumple `commands.ownerAllowFrom` / `allowFrom`.
3. Streaming por bloques desactivado (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. Respuesta de texto final enviada por vía `direct`.

Puedes desactivarlo con `"receipt": false`.

## Configuración

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

O mediante CLI:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Qué binario se usa

`bin` -> `DEJA_BIN` -> `PATH` -> rutas habituales -> copia integrada de `@vshulcz/deja-vu`.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 53 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Privacidad

Índice almacenado en `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`) con redacción automática de `sk-*`, `ghp_*`, `AKIA*` y `Bearer`. Funciona totalmente en local salvo que ejecutes `deja embed`. Configura los 53 agentes compatibles a la vez con `deja install --auto`.

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
