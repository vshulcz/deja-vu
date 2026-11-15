# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S · **13.6 s** para indexar 19,195 sesiones · **la mitad de tokens** que agentmemory) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Estrella en GitHub](https://github.com/vshulcz/deja-vu)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memoria local para [pi](https://github.com/badlogic/pi-mono) alimentada con el historial de sesiones que otros 47 agentes de programación en tu equipo ya escriben en disco (Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes y más). Todo corre mediante un único binario Go (`deja`) con índice BM25 local, sin LLM ni embeddings; solo usa la red en comandos explícitos de actualización/sincronización y en la comprobación diaria desactivable con `DEJA_OFFLINE=1`.

## Instalación

```sh
pi install npm:@vshulcz/pi-deja
```

Si `deja` no está instalado aún, el paquete recurre a la copia incluida siguiendo el orden de [Qué binario se usa](#qué-binario-se-usa). También puedes instalar el binario del sistema antes:

```sh
brew install deja-vu
```

Si `deja install pi-auto` o `deja install --auto` ya instaló la extensión independiente en tu máquina, este paquete cede automáticamente para no registrar herramientas ni inyectar memoria por duplicado. También funciona en `omp` mediante `pi.extensions` y `before_agent_start`.

## Qué hace

- **Inicio de sesión (barra de estado inferior)**: muestra sesiones indexadas y aciertos del proyecto actual.
- **En cada prompt (`before_agent_start`)**: busca contexto previo relevante del proyecto y lo adjunta antes de que responda el modelo.
- **Tras un comando fallido (`tool_result` en `bash` / `powershell`)**: añade el comando que resolvió ese mismo error en sesiones pasadas.
- **Antes de leer o editar (`tool_result` en read/edit/write)**: recuerda decisiones previas asociadas al archivo tocado.
- **Tras compactar contexto (`session_compact`)**: reinyecta el objetivo en curso, decisiones clave y comandos abiertos después del resumen.
- **Al cerrar sesión (`session_shutdown`)**: actualiza el índice local con lo ocurrido en la sesión.
- **`/deja <query>`**: búsqueda interactiva en toda la historia multiagente desde el chat.

## Qué binario se usa

La extensión resuelve el ejecutable `deja` en este orden:

1. La ruta definida en `DEJA_BIN`
2. `deja` disponible en `PATH`
3. Rutas habituales del sistema (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, etc.)
4. El binario incluido dentro de `@vshulcz/deja-vu`

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**la mitad de tokens** que agentmemory en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 48 agentes de programación (el siguiente de 7: 72 s) |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Privacidad

El índice reside en `~/.cache/deja/index.db` (ajustable con `DEJA_INDEX_DIR`) y redacta automáticamente tokens `sk-*`, `ghp_*`, `AKIA*` y cabeceras `Bearer` durante la indexación. No se envía nada fuera de tu equipo salvo si ejecutas manualmente `deja embed` con un endpoint configurado. Configura las herramientas `recall`, `fix`, `blame` y `handoff` en los 48 agentes compatibles con `deja install --auto`.

## Licencia

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT. ¿Te resultó útil? [Dale una estrella a deja-vu en GitHub](https://github.com/vshulcz/deja-vu).
