# `@vshulcz/magpie-deja` — Hook de puerta de enlace Magpie para deja-vu

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S · **13.6 s** para indexar 19,195 sesiones · **la mitad de tokens** que agentmemory) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Estrella en GitHub](https://github.com/vshulcz/deja-vu)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> Inyecta automáticamente contexto de sesiones previas de otros 47 agentes de programación de tu equipo (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes, etc.) en cada petición que atraviesa [Magpie](https://github.com/yetone/magpie).

Funciona en los protocolos OpenAI (`chat`, `responses`), Anthropic (`anthropic`) y Gemini (`gemini`) apoyándose en el binario único en Go `deja` y su índice BM25 local, sin modelos adicionales ni embeddings externos (`DEJA_OFFLINE=1` desactiva además el aviso diario de versión).

## Instalación

```sh
magpie plugin add @vshulcz/magpie-deja
```

Requiere el binario `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` o `install.sh`) y un índice generado con `deja index`. Para habilitar además las herramientas `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` y `handoff` en los 48 agentes compatibles:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

Si la petición ya contiene `<deja-memory>` o `<deja-recall>`, el plugin omite la inyección para no duplicar memoria.

## Cómo funciona y límites de datos

1. **`onRequest` invoca `deja hook-prompt --plain`**: lee `~/.cache/deja/index.db` (configurable mediante `DEJA_INDEX_DIR`) con límite de `800ms` e inserta `<deja-memory>`. **Aviso de petición saliente**: el bloque recuperado viajará hacia el proveedor LLM externo al enviar la solicitud.
2. **Aislamiento por proyecto con `ctx.cwd`**: mantiene `allowUnscoped: false` y nunca pasa `process.cwd()` del propio proxy a `deja hook-prompt`.
3. **Solo origen local por defecto**: `recallRemote: false` restringe la activación a `ctx.origin === "local"` o `ctx.origin === "magpie-key"`.
4. **Doble redacción de secretos**: además de limpiar el índice en origen, aplica `maskSecrets` (alineado con `deja.hooks.mjs`) sobre `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, credenciales en URL y `api_key=...`.
5. **Sin bucles ni invalidación de caché**: ignora mensajes con `<deja-memory`, `<deja-recall>` o `[deja —` y conserva intacta la caché de prefijo del prompt.

## Opciones

| Opción | Valor por defecto | Descripción |
|---|---|---|
| `requestTimeoutMs` | `800` | Tiempo máximo por consulta a `deja` (`50`–`5000` ms) |
| `maxBytes` | `4000` | Límite de bytes inyectados (`256`–`32768`) |
| `recallRemote` | `false` | Permite recordar también en peticiones remotas |
| `allowUnscoped` | `false` | Permite consultar sin `ctx.cwd` definido |
| `cwd` | `""` | Sobrescribe explícitamente el directorio de proyecto |
| `skipAgents` | `[]` | Agentes excluidos de la inyección (p. ej., `["claude-code"]`) |
| `bin` | `""` | Ruta al ejecutable `deja` |

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**la mitad de tokens** que agentmemory en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 48 agentes de programación (el siguiente de 7: 72 s) |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT. ¿Te resultó útil? [Dale una estrella a deja-vu en GitHub](https://github.com/vshulcz/deja-vu).
