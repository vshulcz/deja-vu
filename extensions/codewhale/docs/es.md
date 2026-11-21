# deja-vu para CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Paquete de plugin compatible con Claude para CodeWhale: expone el servidor MCP `deja` (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), la skill `deja-history` y `/deja` sobre sesiones de 50 herramientas de programación con IA.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalación

Requisitos: tener [`deja`](https://github.com/vshulcz/deja-vu#install) instalado en `PATH`.

Instala este paquete en CodeWhale desde el subdirectorio del repositorio (`extensions/codewhale`) usando `/plugin install`.

Como el cargador de compatibilidad con plugins Claude de CodeWhale importa servidores MCP, skills y comandos slash pero ignora `hooks` y `lspServers`, ejecuta `deja install codewhale-auto` (o `deja install --auto`) si también quieres los hooks nativos de ciclo de vida en `~/.codewhale/config.toml` (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`; `deja install codewhale` solo configura MCP y `tools.always_load`):

```sh
deja install codewhale-auto
```

## Contenido del paquete

- `.claude-plugin/plugin.json`: metadatos del plugin (`name: deja-vu`)
- `.mcp.json`: entrada de servidor stdio `deja mcp`
- `skills/deja-history/SKILL.md`: instrucciones de skill para los modos de `deja`
- `commands/deja.md`: comando slash `/deja`

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 50 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

[MIT](../LICENSE)
