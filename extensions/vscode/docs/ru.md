# deja-vu для VS Code, Cursor, Windsurf и Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

**Ваши агенты больше не отлаживают с нуля то, что вы уже починили.**

[deja-vu](https://github.com/vshulcz/deja-vu) локально индексирует историю сессий, которую Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code и ещё 41 кодинг-агент уже сохраняют на диск — один Go-бинарник, без LLM и без фонового демона.

**[★ Поставить звезду на GitHub](https://github.com/vshulcz/deja-vu)** · **[Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Что делает расширение

Показывает живую статистику `deja statusline` в строке состояния IDE (число проиндексированных сессий по агентам, число выданных подсказок за сегодня и историю рабочего пространства). Если `deja` ещё не установлен, клик по индикатору запускает `npx -y @vshulcz/deja-vu install --auto` в терминале и подключает все найденные агенты.

## Быстрая установка

```sh
brew install deja-vu && deja install --auto
# или без Homebrew:
npx -y @vshulcz/deja-vu install --auto
```

Установка расширения строки состояния из терминала:

```sh
code --install-extension vshulcz.deja-status
```

## Измеренный эффект

| Бенчмарк | Результат |
|---|---|
| Точность на LongMemEval-S (470 очищенных вопросов) | **97.2% R@5**, **88.9% hit@1** (MemPalace 96.6%, agentmemory 95.2%) |
| Расход токенов на повторных задачах в репозитории | **На 58% меньше токенов** — вдвое меньше agentmemory (`52,815` против `103,443` без памяти, 11 пар) |
| Задержка лексического поиска в процессе | Медиана **0.7–0.8 мс** по 49 кодинг-агентам |
| Первичная индексация 19,195 реальных сессий | **13.6 с**, без API-ключей (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Лицензия

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT. Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
