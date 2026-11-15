# deja-vu для CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

Claude-совместимый плагин-бандл для CodeWhale: подключает MCP-сервер `deja` (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), навык `deja-history` и команду `/deja` по сессиям из 48 инструментов ИИ-разработки.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Установка

Требования: бинарник [`deja`](https://github.com/vshulcz/deja-vu#install) в `PATH`.

Установите бандл в CodeWhale из подкаталога репозитория (`extensions/codewhale`) командой `/plugin install`.

Загрузчик совместимости плагинов Claude в CodeWhale импортирует MCP-серверы, навыки и слэш-команды, но игнорирует `hooks` и `lspServers`. Если нужны ещё и нативные хуки жизненного цикла CodeWhale (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) в `~/.codewhale/config.toml`, выполните `deja install codewhale-auto` (или `deja install --auto`; `deja install codewhale` настраивает только MCP и `tools.always_load`):

```sh
deja install codewhale-auto
```

## Содержимое бандла

- `.claude-plugin/plugin.json`: метаданные плагина (`name: deja-vu`)
- `.mcp.json`: конфигурация stdio-сервера `deja mcp`
- `skills/deja-history/SKILL.md`: инструкции навыка для режимов `deja`
- `commands/deja.md`: слэш-команда `/deja`

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
