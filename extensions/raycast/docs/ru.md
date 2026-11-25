# deja-vu для Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Поиск и продолжение сессий кодинг-агентов для Claude Code, Codex, Cursor, OpenCode, Gemini CLI и ещё 48 инструментов прямо из Raycast.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Требования

Установите [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Команды

- **Search Sessions**: полнотекстовый поиск по всем сессиям кодинг-агентов на Mac (`deja search --json --limit 25 -- <query>`) с разделением точных совпадений и ближайших по релевантности.
- **Recent Sessions**: просмотр 100 последних сессий по всем установленным агентам (`deja last 100 --json`) с превью метаданных (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Действия

- **Show Session**: просмотр очищенной от секретов переписки прямо в Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: запуск нативной команды продолжения сессии (`deja resume <id>`) в `Terminal.app`.
- **Copy Resume Command**, **Copy Session ID** и **Open Project Folder**.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах) | **на 58% меньше токенов** (на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 53 кодинг-агентам |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Локальная разработка

```sh
cd extensions/raycast && npm install && npx ray develop
```

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT.
