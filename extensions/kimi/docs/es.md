# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) indexa los archivos de sesión que los agentes de código ya escriben en disco —Claude Code, Codex, Cursor, opencode y 45 más— y responde a partir de ellos. Este plugin lleva ese índice a Kimi Code: el recuerdo llega junto al prompt y el propio agente puede consultar el historial.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalación

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Esa forma fija la última release y guarda su origen, que es lo que lee la comprobación de actualizaciones de Kimi. También existe el archivo `kimi-deja.zip` (~10 KB en lugar de todo el repositorio) para catálogo o instalaciones ligeras.

Instala el binario si aún no lo tienes (y opcionalmente conecta el resto de agentes de esta máquina):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Los plugins cargan desde la copia gestionada, así que ejecuta `/reload` o `/new` tras instalar.

## Actualizaciones

Kimi solo notifica actualizaciones de plugins instalados desde su marketplace oficial. Para una instalación desde repositorio, volver a ejecutar `/plugins install` descarga la versión actual, y `deja doctor` avisa si tu copia va por detrás de la que incluye este deja:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## Qué obtienes

- **Los hooks que escribe `deja install kimi-auto`**: en el primer prompt de una sesión, lo acordado en el proyecto (`deja hook-context --once`). En cada prompt, recuerdo pertinente para lo consultado (`deja hook-prompt`); Kimi lo añade al turno, y nada cuando no hay coincidencia. Antes de compactar lee los turnos salientes para conservar lo que estaba en curso (`deja hook-precompact`). En `SessionEnd` limpia la marca activa de la sesión (`deja hook-session-end`).
- **Herramientas**: declara `deja mcp` como servidor MCP con la herramienta única `deja` y modos `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` o `handoff`.
- **`/deja:recall <query>`** para preguntar directamente al historial.
- **La skill `deja-history`**, cargada al inicio para que el agente revise la memoria antes de volver a depurar un problema.

## Qué binario se usa

Por orden: `DEJA_BIN`, el binario que instalaste por tu cuenta (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`) y después `deja` en `PATH`. Tu `deja update` o `brew upgrade deja-vu` prevalece siempre.

## Si también ejecutaste `deja install kimi`

La CLI escribe el mismo servidor MCP en `~/.kimi-code/mcp.json` y el mismo hook en `config.toml`. Como Kimi lanza los servidores de plugin bajo su propio espacio de nombres (`plugin-deja:deja`), correrían ambos: herramientas duplicadas y recuerdo añadido dos veces.

Este plugin comprueba al dispararse el hook y al arrancar el servidor si existe la configuración del instalador y se aparta cuando la encuentra —la copia de `deja install` es la que se mantiene al día—. `deja uninstall kimi` devuelve la titularidad al plugin sin reinstalar nada.

`deja doctor` informa `plugin` para kimi cuando la conexión la aporta el plugin, para no reportar falsos problemas.

Dos cosas que no puede detectar: una entrada MCP añadida a mano bajo un nombre distinto de `deja` (se ejecutaría en paralelo) y un hook escrito manualmente que llame a deja sin el comentario marcador del instalador.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 50 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

MIT

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
