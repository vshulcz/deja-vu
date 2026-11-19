# `@vshulcz/magpie-deja` — хук шлюза Magpie для deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> Автоматически добавляет в запросы через шлюз [Magpie](https://github.com/yetone/magpie) релевантную память из локальных сессий 48 других кодинг-агентов (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes и др.).

Работает поверх протоколов OpenAI (`chat`, `responses`), Anthropic (`anthropic`) и Gemini (`gemini`) через единый Go-бинарник `deja` и локальный BM25-индекс без дополнительных LLM и эмбеддингов (`DEJA_OFFLINE=1` отключает ежедневную проверку обновлений).

## Установка

```sh
magpie plugin add @vshulcz/magpie-deja
```

Требуются установленный бинарник `deja` (`@vshulcz/deja-vu`, `brew install deja-vu` или `install.sh`) и индекс, собранный командой `deja index`. Чтобы подключить инструменты `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` и `handoff` ко всем 49 агентам:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

Если в запросе уже присутствуют `<deja-memory>` или `<deja-recall>`, плагин пропускает вставку во избежание дублирования.

## Архитектура и границы данных

1. **`onRequest` вызывает `deja hook-prompt --plain`**: читает `~/.cache/deja/index.db` (путь задаётся `DEJA_INDEX_DIR`) с таймаутом `800ms` и добавляет блок `<deja-memory>`. **Предупреждение об исходящих запросах**: найденные фрагменты передаются вместе с запросом выбранному внешнему LLM-провайдеру.
2. **Строгая привязка к `ctx.cwd`**: при `allowUnscoped: false` запрос не откатывается к `process.cwd()` самого шлюза при вызове `deja hook-prompt`.
3. **Ограничение по локальному источнику**: по умолчанию `recallRemote: false`, поэтому поиск срабатывает только для `ctx.origin === "local"` и `ctx.origin === "magpie-key"`.
4. **Двухуровневое редактирование секретов**: очистка на этапе индексации и повторный фильтр `maskSecrets` (синхронизированный с `deja.hooks.mjs`) для `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, учётных данных в URL и `api_key=...`.
5. **Защита от циклов и сохранение prefix cache**: сообщения с `<deja-memory`, `<deja-recall>` и `[deja —` пропускаются, а кэш префикса системного промпта не сбрасывается.

## Настройки

| Параметр | По умолчанию | Описание |
|---|---|---|
| `requestTimeoutMs` | `800` | Таймаут вызова `deja` (`50`–`5000` мс) |
| `maxBytes` | `4000` | Максимальный размер вставки (`256`–`32768` байт) |
| `recallRemote` | `false` | Разрешить поиск для удалённых запросов |
| `allowUnscoped` | `false` | Разрешить поиск без `ctx.cwd` |
| `cwd` | `""` | Явное переопределение рабочего каталога |
| `skipAgents` | `[]` | Исключённые агенты (например, `["claude-code"]`) |
| `bin` | `""` | Путь к исполняемому файлу `deja` |

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах) | **на 58% меньше токенов** (на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 49 кодинг-агентам |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Лицензия

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT.
