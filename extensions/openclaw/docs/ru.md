# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Подключает к [OpenClaw](https://openclaw.ai) историю сессий 51 другого кодинг-агента (Claude Code, Codex, Cursor, Gemini, Zed и др.). Использует единый Go-бинарник `deja` и локальный BM25-индекс без LLM (`DEJA_OFFLINE=1` отключает ежедневную проверку обновлений).

## Установка

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

Рекомендуется установить бинарник в систему:

```sh
brew install deja-vu
```

Если ранее уже запускались `deja install openclaw-auto` или `deja install --auto`, плагин автоматически уступает им место и не дублирует инъекцию.

## Возможности

- **`agent:bootstrap` (включая `openclaw agent --local`)**: загружает принятые в проекте решения на старте сессии.
- **`before_prompt_build` (в OpenClaw `2026.8.1` при `hooks.allowConversationAccess: true`)**: подставляет релевантный прошлый опыт перед ответом.
- **`before_compaction`**: сохраняет задачу, выводы и незакрытые команды до сжатия контекста.
- **`session_end`**: обновляет локальный индекс после закрытия сессии.
- **Прослойка результатов инструментов**: при ошибке команды или работе с файлом напоминает прошлые фиксы и договорённости.
- **Инструменты `deja_recall` / `deja_fix` / `deja_blame`**: явный поиск, подбор исправляющей команды и атрибуция файла.
- **`/deja <query>`**: быстрый поиск по истории всех агентов прямо из сообщения.

### Квитанция источника в личных сообщениях

В подходящих личных ответах `reply_payload_sending` добавляет внизу строку курсивом:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

Строка добавляется только при выполнении всех четырёх условий:

1. Личный чат, не группа (`isGroup: false` / `direct`).
2. Отправитель разрешён через `commands.ownerAllowFrom` / `allowFrom`.
3. Потоковая отправка блоками отключена (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. Это итоговый текстовый ответ в канале `direct`.

Отключается параметром `"receipt": false`.

## Конфигурация

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

Или командой CLI:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## Поиск бинарника

`bin` -> `DEJA_BIN` -> `PATH` -> стандартные пути -> копия в `@vshulcz/deja-vu`.

## Бенчмарки

| Полнота (Recall) | Расход токенов | Индексация и поиск |
|---|---|---|
| **97.2% R@5** на LongMemEval-S (88.9% hit@1 на 470 вопросах) | **на 58% меньше токенов** (на повторных задачах) | **13.6 с** на индекс 19,195 сессий / **0.7 мс** на поиск по 52 кодинг-агентам |

## Память в репозитории и передача сессии (`handoff`)

Делитесь проверенными решениями и тупиковыми ветками через git в `.deja/memory.jsonl` (`deja repo candidates`, `deja repo add <id>`). Записи очищаются от секретов при записи, хранятся отсортированными с `merge=union` для беспроблемного слияния веток и подгружаются у каждого, кто клонирует репозиторий (`{"activations":{"auto":{"repo":false}}}` в политике доверия отключает подстановку через хуки). [Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Переключаетесь между инструментами посреди задачи? Выполните `deja handoff --to <agent>` (или вызовите `handoff` через MCP), чтобы передать цель сессии, принятые решения, затронутые файлы, пройденные проверки и оставшуюся работу другому агенту.

## Приватность

Индекс хранится в `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`), токены `sk-*`, `ghp_*`, `AKIA*` и `Bearer` вырезаются при индексации. Без явного запуска `deja embed` данные никуда не передаются. Для подключения всех 52 агентов сразу выполните `deja install --auto`.

Часть [deja-vu](https://github.com/vshulcz/deja-vu). Лицензия MIT.
