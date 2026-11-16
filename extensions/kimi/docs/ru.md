# deja для Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S · **13.6 с** на индекс 19,195 сессий · **вдвое меньше токенов**, чем у agentmemory) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Звезда на GitHub](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu) индексирует файлы сессий, которые кодинг-агенты уже записывают на диск — Claude Code, Codex, Cursor, opencode и ещё 44 инструмента — и отвечает по ним. Этот плагин встраивает индекс в Kimi Code: релевантная память приходит вместе с промптом, а агент умеет сам искать по истории.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Установка

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

Такой вид фиксирует последний релиз и сохраняет источник для проверки обновлений в Kimi. Отдельно выпускается архив `kimi-deja.zip` (~10 КБ вместо всего репозитория) для маркетплейса или лёгкой установки.

Установите бинарник заранее, если его ещё нет (и при желании сразу подключите остальных агентов на машине):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Плагины загружаются из управляемой копии, поэтому после установки выполните `/reload` или `/new`.

## Обновления

Kimi уведомляет об обновлениях только для плагинов из своего маркетплейса. При установке из репозитория повторный `/plugins install` подтягивает свежий релиз, а `deja doctor` подсказывает, если установленная копия отстаёт от версии этого deja:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## Что входит в плагин

- **Хуки, аналогичные `deja install kimi-auto`**: на первом промпте сессии передаются договорённости проекта (`deja hook-context --once`); на каждом промпте — поиск по вопросу (`deja hook-prompt`), Kimi добавляет его к ходу или молчит, если совпадений нет; перед компакцией считываются сворачиваемые ходы, чтобы следующий промпт сохранил контекст незаконченной работы (`deja hook-precompact`); на `SessionEnd` снимается активная метка сессии (`deja hook-session-end`).
- **Инструменты**: объявляет `deja mcp` как MCP-сервер с одним инструментом `deja` и режимами `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` или `handoff`.
- **`/deja:recall <query>`** — для прямого обращения к истории.
- **Скилл `deja-history`**, загружаемый при старте сессии, чтобы агент сначала проверял прошлое, прежде чем снова разбираться с проблемой.

## Выбор бинарника

По порядку: `DEJA_BIN`, установленный вами `deja` (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), затем `deja` в `PATH`. Ваши `deja update` и `brew upgrade deja-vu` всегда имеют приоритет.

## Если вы также запускали `deja install kimi`

CLI записывает тот же MCP-сервер в `~/.kimi-code/mcp.json` и тот же хук в `config.toml`. Kimi запускает сервер плагина в отдельном пространстве имён (`plugin-deja:deja`), поэтому оба варианта сработали бы дважды.

Плагин при срабатывании хука и запуске сервера проверяет конфигурацию установщика и уступает место, если она найдена — ведь именно копию `deja install` обновляет CLI. Команда `deja uninstall kimi` возвращает управление плагину без переустановки.

`deja doctor` выводит `plugin` для kimi, когда интеграцию обеспечивает сам плагин, не считая это ошибкой настройки.

Две вещи он увидеть не может: добавленную вручную запись MCP под именем, отличным от `deja`, и самописный хук, вызывающий deja без маркерного комментария установщика.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах; у MemPalace заявлено 96.6%, у agentmemory — 95.2%) | **на 58% меньше токенов** (**вдвое меньше токенов**, чем у agentmemory на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 48 кодинг-агентам (следующий из 7 инструментов: 72 с) |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Лицензия

MIT

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT. Пригодилось? [Поставьте deja-vu звезду на GitHub](https://github.com/vshulcz/deja-vu).
