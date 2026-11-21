# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Локальная память для [opencode](https://opencode.ai) по истории сессий ещё 51 кодинг-агента (Claude Code, Codex, Cursor, Gemini, Hermes, Zed и др.). Работает на едином бинарнике `deja` и локальном BM25 без LLM (`DEJA_OFFLINE=1` отключает ежедневную проверку обновлений).

## Установка

Добавьте в `opencode.ai/config.json`:

```json
{ "plugin": ["opencode-deja"] }
```

(Для opencode 2.x используйте `"plugins": ["opencode-deja"]` или команду `opencode plugin add opencode-deja`.) Либо выполните `deja install --auto` — он сам настроит `opencode.json` и `plugins/deja.js`.

## Возможности

### Шесть инструментов для агента

| Инструмент | Назначение |
|---|---|
| `deja_recall` | Поиск по прошлым сессиям, ошибкам и символам |
| `deja_session` | Чтение контекста вокруг найденного фрагмента |
| `deja_blame` | Определение сессии, создавшей файл или `path:line` |
| `deja_fix` | Поиск команды, чинившей аналогичную ошибку раньше |
| `deja_how` | Уточнение точных флагов запуска в данном проекте |
| `deja_remember` | Фиксация принятого решения |

### Четыре режима автоподгрузки

- **Старт сессии**: краткий toast в 1.x и скрытое системное сообщение в 2.x вида `deja-vu — from Codex CLI, 4 months ago: "…"`.
- **Кулдаун 20 минут и пропуск субагентов**: не дублирует подсказки в одном каталоге и не засоряет контекст субагентов.
- **Решения по ошибкам и файлам**: добавляет прошлый фикс после падения команды и заметки по затронутым файлам.
- **Перед компакцией**: сохраняет текущую задачу, выводы и открытые команды.

## Параметры

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(Для 2.x используется ключ `"plugins"`.)

- `autoRecall`: включать ли автоинъекцию (`true` по умолчанию).
- `tools`: регистрировать ли шесть инструментов (`true` по умолчанию).
- `bin`: явный путь к бинарнику `deja`.

Чтобы настроить все 8 режимов MCP (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) сразу для всех 52 агентов, запустите `deja install --auto` ([смена агента без потери контекста](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## Выбор бинарника

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> копия внутри npm-пакета.

## Приватность

Индекс хранится в `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), а токены `sk-*`, `ghp_*`, `AKIA*` и `Bearer` удаляются при индексации. Без явного запуска `deja embed` сетевых запросов нет.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах) | **на 58% меньше токенов** (на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 52 кодинг-агентам |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## ## Лицензия

MIT
