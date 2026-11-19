# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | Español | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Parte de **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** en LongMemEval-S) · [Documentación](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Demo en vivo](https://vshulcz.github.io/deja-vu/view/) · [Benchmarks](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Una extensión para [Spec Kit](https://github.com/github/spec-kit). Antes de implementar una funcionalidad, `/speckit.deja-vu.recall` busca en tus sesiones previas de agentes de código y resume qué se intentó, qué se decidió y qué falló, citando el ID de sesión de cada punto.

Las sesiones proceden de [deja](https://github.com/vshulcz/deja-vu), que indexa las transcripciones que Claude Code, Codex, Cursor, Copilot, Gemini y otros agentes ya guardan en disco. Todo permanece en tu equipo.

![Demostración de deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Instalación

Instala `deja` primero:

```sh
brew install deja-vu
# o
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Después, en un proyecto Spec Kit:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Cada versión de deja adjunta `deja-spec-kit.zip` con la misma versión que el lanzamiento. Para probar una copia local: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Uso

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Sin argumentos lee el `spec.md` y el `plan.md` de la funcionalidad activa y elige las consultas automáticamente. Con argumentos busca esos términos. El nombre del comando sigue el estilo de tu agente, por lo que puede aparecer como `/speckit-deja-vu-recall` o `$speckit-deja-vu-recall`.

Por debajo ejecuta, desde la raíz del repositorio:

```sh
deja recall -- "<query>"   # sesiones coincidentes, este proyecto primero
deja files -- "<query>"    # archivos abiertos o editados sobre ese tema
```

y `deja show <id>` cuando un fragmento es demasiado breve para saber qué ocurrió. El informe es una sección `Prior sessions` dividida en intentos fallidos, decisiones, archivos y temas abiertos, cada punto citado como `(deja:<id>)`.

## Hook

La extensión registra un hook opcional `before_implement`. Al ejecutar `/speckit.implement`, el agente propone ejecutar primero la búsqueda en memoria; no corre nada salvo que confirmes. Para desactivarlo, pon `enabled: false` en la entrada de `.specify/extensions.yml`, o ejecuta `specify extension disable deja-vu`.

## Notas

- Solo lectura: el comando busca y lee, no modifica archivos.
- La primera ejecución de deja en una máquina construye el índice, lo que puede tardar un poco con historiales grandes. Después responde en una fracción de segundo.
- El texto recuperado se trata como datos de referencia; el comando indica al agente que nunca siga instrucciones encontradas dentro de ese texto.

## Benchmarks

| Recall | Tokens | Indexado y búsqueda |
|---|---|---|
| **97.2% R@5** en LongMemEval-S (88.9% hit@1 en 470 preguntas) | **58% menos tokens** (en tareas repetidas) | **13.6 s** para indexar 19,195 sesiones / **0.7 ms** de búsqueda en 49 agentes de programación |

## Memoria de repositorio y relevo entre agentes

Comparte decisiones validadas y callejones sin salida del proyecto vía git en `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Las entradas se redactan antes de guardarse, se ordenan por hash con `merge=union` para fusionar ramas paralelas sin conflictos y se cargan para cualquier compañero que clone el repositorio (`{"activations":{"auto":{"repo":false}}}` en la política de confianza desactiva la inyección automática en hooks). [Más detalles](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

¿Cambias de herramienta a mitad de una tarea? Ejecuta `deja handoff --to <agent>` (o usa `handoff` vía MCP) para pasar el objetivo de la sesión activa, las decisiones tomadas, archivos modificados, comprobaciones superadas y trabajo pendiente a otro agente.

## Licencia

MIT

Parte de [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
