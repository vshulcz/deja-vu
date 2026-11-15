# deja-vu для Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | Русский | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Часть **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** на LongMemEval-S · **13.6 с** на индекс 19,195 сессий · **вдвое меньше токенов**, чем у agentmemory) · [Документация](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Живое демо](https://vshulcz.github.io/deja-vu/view/) · [Бенчмарки](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ Звезда на GitHub](https://github.com/vshulcz/deja-vu)**

Расширение для [Spec Kit](https://github.com/github/spec-kit). Перед реализацией фичи команда `/speckit.deja-vu.recall` ищет по прошлым сессиям ваших кодинг-агентов и показывает, что уже пробовали, что решили и что не сработало, со ссылкой на ID сессии для каждого пункта.

Сессии поступают из [deja](https://github.com/vshulcz/deja-vu), который индексирует логи на диске от Claude Code, Codex, Cursor, Copilot, Gemini и других агентов. Все данные остаются у вас на машине.

![Демонстрация deja-vu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Установка

Сначала установите `deja`:

```sh
brew install deja-vu
# или
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Затем в проекте Spec Kit:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

К каждому релизу deja приложен архив `deja-spec-kit.zip` той же версии. Чтобы подключить локальную копию репозитория: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Использование

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Без аргументов команда читает `spec.md` и `plan.md` текущей задачи и сама выбирает поисковые запросы. С аргументами — ищет по переданной фразе. Имя слэш-команды зависит от формата вашего агента и может выглядеть как `/speckit-deja-vu-recall` или `$speckit-deja-vu-recall`.

Под капотом из корня репозитория запускаются:

```sh
deja recall -- "<query>"   # подходящие сессии (сначала текущий проект)
deja files -- "<query>"    # файлы, которые открывали или меняли по этой теме
```

и `deja show <id>`, если фрагмент слишком короткий для понимания. Итоговый отчёт — секция `Prior sessions` с неудачными попытками, принятыми решениями, файлами и открытыми вопросами, где каждый пункт снабжён ссылкой `(deja:<id>)`.

## Хук (Hook)

Расширение регистрирует один необязательный хук `before_implement`. При запуске `/speckit.implement` агент предлагает сначала выполнить поиск по памяти и запускает его только после вашего согласия. Чтобы отключить, укажите `enabled: false` в `.specify/extensions.yml` или выполните `specify extension disable deja-vu`.

## Примечания

- Только чтение: команда ищет и читает, ничего не редактируя.
- Первый запуск deja на машине строит индекс, что при большом архиве может занять некоторое время; все последующие запросы отвечают заметно быстрее секунды.
- Найденный текст сессий считается справочными данными: команда прямо предписывает агенту никогда не выполнять инструкции, встреченные внутри прошлых логов.

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
