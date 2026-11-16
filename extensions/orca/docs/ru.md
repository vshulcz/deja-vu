# deja-vu для Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Плагин Orca v2 (`trusted-worker`) для `deja` — даёт память по сессиям из 48 инструментов ИИ-разработки (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca и других), включая историю, записанную ещё до установки `deja`.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Установка

Требования: [`deja`](https://github.com/vshulcz/deja-vu#install) в `PATH` (или в `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin`, `%LOCALAPPDATA%\Programs\deja`).

Установите каталог плагина `extensions/orca` в Orca (`>=1.4.248`).

Чтобы также настроить в Orca нативный MCP, файлы навыков, `SessionStart`, `PreCompact`, `SessionEnd` и подсказки `fix` при ошибках команд, выполните:

```sh
deja install orca-auto
```

(`deja install orca` ставит MCP, навык и `/deja` без хуков жизненного цикла; `deja install orca-auto` или `deja install --auto` подключает весь набор хуков.)

Если `deja install orca-auto` уже прописал нативный хук `UserPromptSubmit` в `settings.json` Orca, этот worker уступает автоматически, чтобы блоки `<deja-recall>` не дублировались.

## Что добавляет плагин

- **Хуки**
  - `agent.prompt.before`: определяет каталог активной рабочей области и подставляет релевантный контекст `<deja-recall>` в промпт в рамках 2-секундного лимита Orca (таймаут `1.3s`, fail-open).
  - `agent.tool.before`: при запуске субагентов `Agent` или `Task` дописывает `<deja-recall>` только в `input.prompt` (входы `Bash`, `Read`, `Edit` и других инструментов не изменяются).
  - `agent.tool.after`: в API Orca работает только в режиме наблюдения (`next` игнорируется и хост не ждёт), поэтому подсказки `fix` при сбое команд приходят от `deja install orca-auto`.
- **Команды**
  - `deja.orient` (`deja: Orient on workspace`): запускает `deja hook-context --plain` для текущей рабочей области и показывает краткую сводку.
  - `deja.recall` (`deja: Recall past sessions`): выполняет `deja search --limit 5 -- <query>` (или `deja last -n 5`, если запрос не задан) и выводит результат.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах; у MemPalace заявлено 96.6%, у agentmemory — 95.2%) | **на 58% меньше токенов** (**вдвое меньше токенов**, чем у agentmemory на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 48 кодинг-агентам (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Лицензия

[MIT](../LICENSE)

Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
