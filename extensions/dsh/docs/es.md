# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Parte de **[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Plugin `session-query` para DeepSeek Harness (`dsh`) y DSH Desktop (`anywhere-labs/dsh-desktop`). Junto con tus conversaciones de DSH, indexa en local el historial de otros 48 agentes de programación en tu equipo (Claude Code, Codex, Cursor, Gemini, Hermes, etc.) usando el binario único en Go `deja` y BM25 (`DEJA_OFFLINE=1` desactiva la comprobación diaria).

## Instalación

Incluye siempre `--profile` al añadir el plugin:

```sh
dsh plugin --profile web add dsh-deja
# o desde este repositorio:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

Si aún no tienes `deja` instalado, define `DEJA_BIN` o instala el binario y configura los 49 agentes junto con `$DSH_HOME/plugins/deja/`:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## Qué añade

### Seis herramientas para el agente

| Herramienta | Propósito |
|---|---|
| `deja_recall` | Buscar sesiones previas por texto, error o bandera CLI |
| `deja_session` | Recuperar los turnos completos alrededor de un resultado |
| `deja_blame` | Averiguar qué sesión tocó un archivo o `path:line` |
| `deja_fix` | Consultar el comando que reparó antes ese mismo fallo |
| `deja_how` | Recordar los comandos y flags reales usados en el repo |
| `deja_remember` | Guardar una decisión cerrada del proyecto |

- **Comando `/deja <...>`**: búsqueda directa desde la barra de entrada.
- **Superficies soportadas**: compatible con `deja install dsh-auto`, `dsh web`, `tui`, `headless` y `sdk` (`autoRecall: false` en YAML desactiva la inyección automática).

## Controles y atajos de búsqueda

Desde `Settings → Deja`:

- **Mod+F** (o **Mod+Alt+F** / **Cmd+Option+F** en macOS): abre la búsqueda; puedes acotar con `/deja --session <bare session UUID> -- <your draft>`.
- **Mod+Shift+K** (**Cmd+Shift+K** en macOS): búsqueda global entre todos los agentes.
- **Mod+K**: abre el selector con el conmutador **DSH conversations only** (hasta 20 resultados).
- **Atajos alternativos en navegadores Linux**: **`Mod+/`**, **`Mod+Shift+,`** y **`Mod+Shift+.`** (`shortcuts.open`).

## Personalización de atajos

En `Settings → Deja`, junto a **`session.search`**, usa **Change shortcut**, **Restore default** o **Remove**. El **dsh-deja package host plugin** expone `/api/deja-search`; si `/api/deja-search?probe=1` devuelve `{"ok": true, "plugin": "dsh-deja"}`, la conexión con `deja search` está activa.

## Cómo está conectado

- **Contexto de sistema**: usa `ctx.systemPrompt.context` y `agent/pre-step` (desde `dsh 0.1.1-rc.2`) para enviar `additionalContext` y `systemMessage`.
- **Rutas HTTP y límites**: `/api/deja-notes` (`--plain`) alimenta a `client.js`; `GET /api/deja-search` ejecuta `deja search --harness deepseek --json --limit 20 -- <query>` con 20 segundos de límite, 2 MiB de salida máxima y 500 caracteres de consulta.
- **Barra lateral**: acoplado mediante `data-slot="sidebar.workspaces"`, `aria-expanded` y `_listArea` (compatible con DSH `0.2.1-rc.5`).

## Probar los controles de búsqueda

```sh
node --test extensions/dsh/test/*.test.js
```

También incluye `extensions/dsh/test/search-browser.html` (React 18 CDN) para pruebas manuales en navegador sin compilación.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 49 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Qué no hace

El índice se guarda exclusivamente en `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), no contacta modelos externos salvo si ejecutas `deja embed`, redacta `sk-*`, `ghp_*`, `AKIA*` y `Bearer`, y nunca modifica los registros originales de sesión.

## Licencia

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
