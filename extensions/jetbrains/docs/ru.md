# deja-vu для JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Локальный поиск по сессиям разных агентов, построчный AI-blame и передача контекста для IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm и Rider (`2024.2+`).

[deja-vu](https://github.com/vshulcz/deja-vu) индексирует локально в SQLite FTS5 файлы сессий, которые записывают Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf и ещё 39 кодинг-агентов — с редактированием секретов перед выводом и без каких-либо облачных сервисов.

**[★ Поставить звезду deja-vu на GitHub](https://github.com/vshulcz/deja-vu)** · **[Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Возможности

- **Виджет в строке состояния**: отображает актуальную статистику `deja statusline` для открытого проекта в правой части статус-бара IDE. Клик открывает всплывающее меню действий `deja-vu`.
- **Поиск по AI-сессиям (`Tools → deja-vu → Search AI Sessions...`)**: ищет по локальным сессиям всех 49 поддерживаемых кодинг-агентов (`deja search --json`) и открывает выбранную запись во вкладке Markdown только для чтения.
- **Недавние AI-сессии (`Tools → deja-vu → Recent AI Sessions`)**: показывает 50 последних сессий на машине (`deja last 50 --json`) с быстрой фильтрацией.
- **AI Blame текущей строки (контекстное меню редактора / `Tools → deja-vu → AI Blame Current Line`)**: запускает `deja blame <file>:<line> --json` (с переходом к `<file>`, если строка не найдена) и открывает AI-сессию, которая создала или изменила строку под курсором.
- **Копирование межагентной передачи (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: выполняет `deja handoff` в корне проекта, копирует сводку в буфер обмена и открывает её во вкладке Markdown только для чтения.

## Установка

1. Установите CLI `deja` и подключите локальных кодинг-агентов:

```sh
brew install deja-vu && deja install --auto
# или без Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. Установите плагин JetBrains ([`com.github.vshulcz.deja-vu` в JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)):

- **Из JetBrains Marketplace**: [установите напрямую с plugins.jetbrains.com](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) или откройте **Settings → Plugins → Marketplace** и найдите `deja-vu`.
- **С диска**: **Settings → Plugins → ⚙️ → Install Plugin from Disk...** и выберите `deja-jetbrains.zip` со страницы [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest).
- **Через CLI**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(Замените `idea` на `goland`, `pycharm`, `webstorm`, `rustrover` или `clion` в зависимости от вашей IDE.)*

## Сборка из исходников

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

Готовый архив сохраняется в `build/distributions/`.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах; у MemPalace заявлено 96.6%, у agentmemory — 95.2%) | **на 58% меньше токенов** (**вдвое меньше токенов**, чем у agentmemory на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 49 кодинг-агентам (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Лицензия

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
