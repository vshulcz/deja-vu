# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S · **13.6 s** para indexar 19,195 sesiones · **la mitad de tokens** que agentmemory) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Estrella en GitHub](https://github.com/vshulcz/deja-vu)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Memoria local para [opencode](https://opencode.ai) compartida con los otros 47 agentes de programación de tu equipo (Claude Code, Codex, Cursor, Gemini, Hermes, Zed, etc.). Funciona con el binario único en Go `deja` y un índice BM25 local sin modelos LLM (`DEJA_OFFLINE=1` desactiva la comprobación diaria).

## Instalación

En `opencode.ai/config.json`:

```json
{ "plugin": ["opencode-deja"] }
```

(En opencode 2.x usa `"plugins": ["opencode-deja"]` o `opencode plugin add opencode-deja`.) O ejecuta `deja install --auto` para configurar `opencode.json` y `plugins/deja.js` automáticamente.

## Qué incluye

### Seis herramientas bajo demanda

| Herramienta | Propósito |
|---|---|
| `deja_recall` | Buscar sesiones pasadas por consulta, error o símbolo |
| `deja_session` | Leer el contexto completo de una sesión recuperada |
| `deja_blame` | Saber qué sesión escribió un archivo o `path:line` |
| `deja_fix` | Ver qué comando resolvió antes el mismo fallo |
| `deja_how` | Recordar cómo se ejecutó un comando o test en el repo |
| `deja_remember` | Guardar una decisión cerrada |

### Cuatro automatismos de recuerdo

- **Aviso al inicio**: toast informativo en opencode 1.x o contexto silencioso en 2.x (`deja-vu — from Codex CLI, 4 months ago: "…"`).
- **Enfriamiento de 20 minutos y omisión en subagentes**: evita repetir recordatorios seguidos en el mismo proyecto y no sobrecarga subagentes.
- **Comandos fallidos y decisiones por archivo**: adjunta arreglos previos ante errores y notas del archivo tratado.
- **Precompactación**: conserva el estado activo cuando el chat resume el historial.

## Opciones

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(Usa `"plugins"` en opencode 2.x.)

- `autoRecall`: activa o desactiva la inyección automática (`true` por defecto).
- `tools`: expone las 6 herramientas (`true` por defecto).
- `bin`: ruta personalizada al binario `deja`.

Para habilitar MCP con los 8 modos (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) en las 48 herramientas compatibles, ejecuta `deja install --auto` (ver [cambiar de agente sin perder contexto](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Qué binario se usa

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> copia incluida en npm.

## Privacidad

Índice en `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` opcional) con limpieza automática de `sk-*`, `ghp_*`, `AKIA*` y `Bearer`. Sin tráfico saliente salvo que configures explícitamente `deja embed`.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas; MemPalace publica 96.6%, agentmemory 95.2%) | **58% menos tokens** (**la mitad de tokens** que agentmemory en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 48 agentes de programación (el siguiente de 7: 72 s) |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## ## Licencia

MIT

¿Te resultó útil? [Dale una estrella a deja-vu en GitHub](https://github.com/vshulcz/deja-vu).
