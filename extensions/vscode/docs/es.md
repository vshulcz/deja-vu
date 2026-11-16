# deja-vu para VS Code, Cursor, Windsurf y Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**Tus agentes dejan de depurar desde cero lo que ya resolviste.**

[deja-vu](https://github.com/vshulcz/deja-vu) indexa localmente los historiales de sesión que Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code y otros 40 agentes ya escriben en disco — en un único binario en Go, sin LLM ni demonio en segundo plano.

**[★ Estrella en GitHub](https://github.com/vshulcz/deja-vu)** · **[Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Qué hace esta extensión

Muestra el estado en vivo de `deja statusline` en la barra de estado del IDE (sesiones indexadas entre agentes, recuerdos servidos hoy e historial del espacio de trabajo). Si aún no está instalado `deja`, hacer clic en el elemento abre el terminal y ejecuta `npx -y @vshulcz/deja-vu install --auto` para conectar todos los agentes detectados.

## Instalación rápida

```sh
brew install deja-vu && deja install --auto
# o sin Homebrew:
npx -y @vshulcz/deja-vu install --auto
```

Instala la extensión de barra de estado desde CLI:

```sh
code --install-extension vshulcz.deja-status
```

## Impacto medido

| Benchmark | Resultado |
|---|---|
| Precisión en LongMemEval-S (470 preguntas depuradas) | **97.2% R@5**, **88.9% hit@1** (MemPalace 96.6%, agentmemory 95.2%) |
| Costo de tokens en trabajo repetido sobre el repositorio | **58% menos tokens** — la mitad que agentmemory (`52,815` vs `103,443` en frío, 11 pares) |
| Latencia de consulta léxica en proceso | Mediana de **0.7–0.8 ms** en 48 agentes de programación |
| Indexado inicial de 19,195 sesiones reales | **13.6 s**, sin claves API (el siguiente de 7 herramientas: 72 s) |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT. ¿Te resultó útil? [Dale una estrella a deja-vu en GitHub](https://github.com/vshulcz/deja-vu).
