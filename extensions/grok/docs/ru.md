# deja для Grok Build

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Долговременная локальная память из вашей истории в Claude Code, Codex, Cursor, opencode, Zed и ещё 48 кодинг-агентах. Вся работа идёт через единый Go-бинарник `deja` на локальном диске (`DEJA_OFFLINE=1` отключает ежедневную проверку обновлений).

## Установка

Сначала установите сам бинарник `deja` (плагин содержит только манифест и хуки):

```sh
brew install deja-vu
# или:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Затем подключите каталог этого репозитория ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

Либо пропишите всё автоматически через `deja install --auto` (`[mcp_servers.deja]` в `~/.grok/config.toml` и `~/.grok/hooks/deja.json`).

## Что входит в пакет

- **MCP-инструмент `deja`** (8 режимов: `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **Навык `deja-history`** и команда **`/deja:recall <query>`**
- **5 хуков (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (для `Bash` на Grok 1.0.41), `PreCompact` и `SessionEnd`

## Совместная работа без дублей

Если в `~/.grok/config.toml` уже есть секция `[mcp_servers.deja]` или в `~/.grok/hooks/deja.json` настроены хуки deja, плагин автоматически отключает дублирующие шаги (`deja uninstall grok` удаляет ручную привязку).

## Поиск бинарника

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## Корректный отказ при отсутствии бинарника

Плагин опирается на `${GROK_PLUGIN_ROOT}` и `.mcp.json`. В Grok Build 1.0.5 без установленного `deja` команды `grok mcp doctor` и `grok inspect` выведут `handshake failed: connection closed`, что сразу исчезает после установки `deja`.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах) | **на 58% меньше токенов** (на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 53 кодинг-агентам |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## ## Лицензия

MIT
