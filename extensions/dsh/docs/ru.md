# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S · **13.6 с** на индекс 19,195 сессий · **вдвое меньше токенов**, чем у agentmemory) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Плагин `session-query` для DeepSeek Harness (`dsh`) и DSH Desktop (`anywhere-labs/dsh-desktop`). Помимо диалогов самого DSH, индексирует на диске историю ещё 48 кодинг-агентов (Claude Code, Codex, Cursor, Gemini, Hermes и др.) через единый Go-бинарник `deja` и локальный BM25 (`DEJA_OFFLINE=1` отключает ежедневную проверку релизов).

## Установка

Обязательно передавайте флаг `--profile`:

```sh
dsh plugin --profile web add dsh-deja
# или напрямую из репозитория:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

Если бинарник `deja` ещё не стоит в системе, укажите `DEJA_BIN` или настройте все 49 агентов и `$DSH_HOME/plugins/deja/`:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## Что добавляет плагин

### Шесть инструментов для агента

| Инструмент | Назначение |
|---|---|
| `deja_recall` | Поиск по прошлым сессиям, ошибкам и флагам |
| `deja_session` | Полные соседние реплики вокруг найденного места |
| `deja_blame` | Определение сессии, менявшей файл или `path:line` |
| `deja_fix` | Команда, устранившая аналогичную ошибку раньше |
| `deja_how` | Реальные флаги сборки и запуска в этом проекте |
| `deja_remember` | Фиксация договорённости в памяти |

- **Команда `/deja <...>`**: поиск прямо из строки ввода.
- **Среды работы**: интегрируется через `deja install dsh-auto` и в `dsh web`, `tui`, `headless`, `sdk` (`autoRecall: false` в YAML отключает автоинъекцию).

## Управление поиском и горячие клавиши

Настраивается в разделе `Settings → Deja`:

- **Mod+F** (или **Mod+Alt+F** / **Cmd+Option+F** на macOS): открыть поиск; для ограничения одной сессией используется `/deja --session <bare session UUID> -- <your draft>`.
- **Mod+Shift+K** (**Cmd+Shift+K** на macOS): глобальный кросс-агентный поиск.
- **Mod+K**: открыть панель с фильтром **DSH conversations only** (до 20 результатов).
- **Запасные комбинации для браузеров в Linux**: **`Mod+/`**, **`Mod+Shift+,`** и **`Mod+Shift+.`** (`shortcuts.open`).

## Переназначение клавиш

В `Settings → Deja` напротив **`session.search`** доступны кнопки **Change shortcut**, **Restore default** и **Remove**. Панель обслуживается хост-плагином **dsh-deja package host plugin** по маршруту `/api/deja-search`; запрос `/api/deja-search?probe=1` возвращает `{"ok": true, "plugin": "dsh-deja"}`, подтверждая связь с `deja search`.

## Как устроена интеграция

- **Хуки промпта**: передаёт `additionalContext` и `systemMessage` через `ctx.systemPrompt.context` и `agent/pre-step` (`dsh 0.1.1-rc.2`+).
- **Маршруты и ограничения**: `/api/deja-notes` (`--plain`) для `client.js`, а `GET /api/deja-search` вызывает `deja search --harness deepseek --json --limit 20 -- <query>` (таймаут 20 секунд, лимит вывода 2 MiB, лимит запроса 500 символов).
- **Слот боковой панели**: `data-slot="sidebar.workspaces"`, `aria-expanded` и `_listArea` (для DSH `0.2.1-rc.5`).

## Тестирование поиска

```sh
node --test extensions/dsh/test/*.test.js
```

Для проверки в браузере без сборки предусмотрен `extensions/dsh/test/search-browser.html` (React 18 CDN).

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах; у MemPalace заявлено 96.6%, у agentmemory — 95.2%) | **на 58% меньше токенов** (**вдвое меньше токенов**, чем у agentmemory на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 49 кодинг-агентам (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Чего плагин не делает

Индекс остаётся только в `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), без явного `deja embed` нет сетевых вызовов, ключи `sk-*`, `ghp_*`, `AKIA*` и `Bearer` удаляются на этапе индексации, а исходные файлы сессий не модифицируются.

## Лицензия

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT. Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
