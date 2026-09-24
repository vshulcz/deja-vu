<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Одна память на все кодинг-агенты, собранная из истории, которая уже лежит у вас на диске.</b></p>

<p align="center">Ваш агент собирается заново отлаживать то, что вы починили в марте — тогда в другом агенте.
deja индексирует сессии, которые Claude Code, Codex, Cursor и остальные агенты на этой машине и так пишут
на диск, и отдаёт нужную любому из них, кто спросит.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="один и тот же вопрос одному и тому же агенту дважды: без памяти он не помнит ничего, с deja отвечает выводом восьмимесячной давности"></p>

<p align="center"><sub><em>Никто ничего не искал — агент вызвал deja сам. Два настоящих прогона, настоящая модель, настоящие вызовы инструментов, на синтетическом корпусе: ничья история не публикуется.</em></sub></p>

<p align="center"><b>deja полна с первой минуты: история, которую 34 агента уже записали, индекс за несколько секунд, без модели и без отдельного шага сбора.</b></p>

<p align="center">
<b>На 58% меньше токенов</b> на задаче, которую эта машина уже решала &middot; <b>88.1% hit@1</b> на LongMemEval-S (очищенный набор из 470 вопросов) &middot; <b>70.5%</b> на LoCoMo &middot; поиск по гигабайтам истории за <b>миллисекунды</b><br>
<sub>По 11 прогонов на плечо: 53,558 токенов против 126,222 без всякой памяти; более поздний прогон того же стенда дал 71% &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">во что обходится одна задача</a> &middot;
обе харнессы для оценки поиска лежат в этом репозитории и прогоняются на публичных датасетах за минуты &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">проверьте цифры сами</a></sub>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | Русский | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Документация</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Замеры</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Сравнение</a></p>
<p align="center"><sub>Если пригодилось — поставьте deja-vu звезду на <a href="https://github.com/vshulcz/deja-vu">GitHub</a>.</sub></p>

## Установка

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Если `raw.githubusercontent.com` недоступен, есть зеркало в npm, версия та же:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Десять секунд на установку, около десяти на индекс. Вторая команда подключает MCP-recall к каждому найденному
агенту, включает отдачу контекста при старте сессии там, где агент это умеет, и строит первый индекс,
чтобы следующая сессия ничего не ждала.

Откройте новую сессию агента и спросите про то, что делали месяцы назад:

> мы уже разбирались с jwt refresh rotation? посмотри в своей памяти

Спрашивать и не обязательно: с включённым авто-recall агент уже на старте сессии знает, что в этом проекте
было решено.

У opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw и pi есть пакеты в их
собственных экосистемах — для тех, кто привык ставить расширения оттуда:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: найдите deja в панели расширений
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` подключает всё перечисленное само, так что любого из двух путей достаточно. Оба сразу
тоже не сломаются: пакет смотрит, что написал `deja install`, и дописывает только недостающее — без дублей
инструментов и без второго recall. Подробности в [`extensions/`](../../extensions).

Тот же поиск существует как skill, который поставит любой агент, читающий `SKILL.md`:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

Skill вызывает установленный бинарник `deja`, своего не несёт.

Другие способы: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, или `npx @vshulcz/deja-vu "запрос"`, чтобы
попробовать ничего не устанавливая. На Windows установочный скрипт выходит с `unsupported OS` — это
shell-скрипт; возьмите `deja-vu_<version>_windows_amd64.zip` из
[последнего релиза](https://github.com/vshulcz/deja-vu/releases/latest) и положите `deja.exe` в `PATH`.

Одного бинарника достаточно для полной работы: индексация, поиск, `show`, `ctx`, `blame`, `--json` и
вычистка секретов не требуют больше ничего. `deja install` подключает MCP к вашим агентам и включает recall
на старте сессии — это полезно, но не обязательно.

## Что это даёт

**Решено в Codex — помнит Claude.** Тридцать четыре кодинг-агента пишут каждый разговор в локальные файлы,
deja превращает эти файлы в слой памяти, который читают все они.

| | |
| --- | --- |
| **Поиск назад во времени** | `deja "connection pool exhausted"` ищет по гигабайтам, включая всё, что было до установки deja. Вопрос на естественном языке откатывается к режиму релевантности. Время — подсказка, а не фильтр. |
| **Recall между агентами** | MCP-инструмент `deja` в режиме `recall` в любом агенте отвечает «это мы чинили три недели назад», кто бы тогда ни чинил. |
| **Переживает компактацию** | Замерено на 43 компактациях: сводка сохранила 77% решений и 0.2% команд, которые вы запускали. Остальные 99.8% возвращает deja. В Claude Code и Codex deja записывает задачу, файлы и команды в момент начала компактации и отдаёт их одним куском в следующей сессии. |
| **Recall в момент действия** | Перед тем как агент правит файл или запускает команду, хук `PreToolUse` называет прежние решения по этому файлу, рабочий вариант этой команды или программу, которой на этой машине просто нет. Когда команда падает, хук `PostToolUse` показывает, что запускали после той же ошибки на этой машине — ровно та пара, про которую агент сам не спросит. |
| **Индексируется работа, а не только слова** | Каждый открытый за ход файл, каждая запущенная команда с кодом возврата и точный фрагмент, который заменила правка. Ровно то, что теряет любая сводка. |

Ещё: `deja promote <id> --state rejected` помечает отменённое решение, и дальше каждое попадание показывает,
что это пробовали и отвергли; попадание сообщает «4 файла этой сессии с тех пор изменились» и молчит, когда
судить не может; `deja sync ssh laptop` переносит память между машинами только дописыванием, без облака
посередине; `deja handoff --to codex` упаковывает текущий контекст, чтобы продолжить в другом агенте;
ключи, токены, JWT и блоки приватных ключей известных форм вырезаются при индексации — но совпадение по
шаблону не детектор секретов, незнакомая форма может пройти.

### Нарисуйте собственную работу

`deja stats --card` рисует прямо в терминале; дайте имя файла — запишет SVG для README вашего профиля.
Чтобы опубликовать в другом месте, [переведите в PNG](https://vshulcz.github.io/deja-vu/card/) — страница
конвертирует в вашем же браузере.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="карточка статистики deja: год сессий тепловой картой, из каких они агентов и какая была самой длинной"></p>

Полный справочник — на [сайте документации](https://vshulcz.github.io/deja-vu/).

## Приватность

Индексация и поиск локальны. Сеть нужна только `deja update`, `deja sync ssh`, проверке версии внутри
`deja doctor` и `deja embed`, который ходит на заданный вами эндпоинт.

Учётные данные вычищаются при индексации: ключи AWS, присваивания `api_key=` и `token=`, bearer-токены и
голые JWT, PEM-блоки приватных ключей, токены разных провайдеров, URL вида `scheme://user:pass@host`,
высокоэнтропийные значения, под которые нет правила, и пароли, написанные прозой — «the admin password
is …», где опереться не на что. Значение становится `[redacted:<kind>]`, текст вокруг остаётся доступным
для поиска. `deja share` и `deja sync export` чистят ещё раз при экспорте. Совпадение по шаблону не детектор
секретов: форма, которой нет в правилах, может пройти как есть — см. модель безопасности.

`deja forget` убирает сессии из перестроенного индекса и оставляет надгробие, так что следующий
`deja index` не поднимет их обратно из исходной истории.
[Модель безопасности](../../docs/SECURITY-MODEL.md) описывает потоки данных, границы вычистки, допущения о
доверии и проверку релизов.

## Командная строка

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Команда | Что делает |
| --- | --- |
| `deja <запрос>` | Ищет по всей истории. Несколько слов — это AND, кавычки требуют точной последовательности; когда точного попадания нет, пробует словоформы и близкие написания. |
| `deja wip` | Чем занималась прошлая сессия в этом каталоге: задача, к чему пришли, открытые файлы, последняя команда и упала ли она. Всё выведено из записей, без опоры на чьи-то заметки. |
| `deja blame <путь>[:строка]` | Какие сессии обсуждали этот файл, что решили и почему. Со строкой: коммит, изменивший её последним, и сессия, написавшая удалённый этим коммитом текст. |
| `deja files <тема>` | В обратную сторону: какие файлы на самом деле трогала работа по теме. |
| `deja how <инструмент>` | Как эту вещь реально запускают на этой машине, с настоящими аргументами, из команд, которые агенты уже выполняли. |
| `deja fix <ошибка>` | Что запускали после той же ошибки на этой машине, и после чего она больше не появлялась. |
| `deja friction` | Ошибки, попавшие больше чем в три разные сессии, с указанием, из каких они инструментов. |
| `deja ctx <запрос>` | Markdown-выжимка лучших попаданий, годная прямо в промпт. |
| `deja resume <id>` | Открывает найденную сессию заново в её родном инструменте. |
| `deja view` | Выгружает всю память в один локальный HTML-файл. Без сервера, ничего не уходит с машины. |
| `deja doctor [--deep]` | Самопроверка; с `--deep` сверяет индекс с исходными файлами. |
| `deja mcp` | MCP-сервер по stdio — тот самый, который подключает `deja install`. |

Полный справочник — в [документации команд](https://vshulcz.github.io/deja-vu/guide/commands.html).

### MCP-инструменты

Сервер отдаёт один инструмент `deja`, режим выбирается параметром `mode`: `recall`, `context`, `blame`,
`fix`, `how`, `remember`. `deja install` подключает его сам — это важно знать только при ручной настройке
агента. Прежние шесть имён инструментов продолжают работать у уже подключённых клиентов.

Один инструмент вместо семи — это про стоимость, а не про стиль. Подключённый MCP-сервер отправляет описания
своих инструментов с каждым запросом, так что за них платят каждый ход независимо от того, вызвал агент
что-нибудь или нет: здесь 477 токенов против 8,283 у самого крупного из восьми измеренных серверов. У самой
deja было 828, пока схему не свели к одному инструменту с режимами.

## Поддерживаемые инструменты

С включённым авто-recall Claude Code и Codex отдают deja текущую запись в момент начала компактации, и она
сохраняет то, что сводка вот-вот потеряет: задачу, выводы, файлы, чем закончилась каждая команда и что
осталось недоделанным. Следующий хук в той же сессии и том же рабочем каталоге возвращает это одним куском,
не больше 4 КБ, со строкой о том, менялся ли репозиторий с тех пор. `deja stats` считает количество вызовов
инструментов между компактацией и первой правкой — этим и меряли.
Подробности во [восстановлении после компактации](../../docs/compaction.md).

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

Что именно каждый из них поддерживает — MCP-recall, авто-recall, skills, команды, resume, handoff — см. в
[матрице возможностей в английском README](../../README.md#supported-harnesses). Нестандартные пути к хранилищам
задаются переменными `DEJA_*_ROOT`, переменные миграции самих инструментов тоже учитываются.

### Агенты со своим пакетом

`deja install --auto` подключает эти так же, как остальные, и это всегда самый короткий путь. Заодно у
каждого есть пакет в своей экосистеме — для тех, кто ставит расширения оттуда:

| Агент | Пакет | Установка |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | плагин `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | плагин `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, затем `codex plugin add deja-vu@deja-vu` |
| Grok Build | плагин `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, затем `grok plugin install deja` |
| OpenClaw | ClawHub и npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (и omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

Любого пути достаточно, оба вместе тоже не мешают: каждый пакет сначала читает то, что записал
`deja install`. opencode, dsh и OpenClaw дописывают только недостающее; Kimi, Grok, Codex и pi уступают,
если установщик уже всё подключил; в Zed обе стороны используют один и тот же id сервера. Так что порядок
установки значения не имеет.

Все они используют уже установленный вами deja, своя копия внутри пакета — только запасной вариант.

## Семантический recall, по желанию

Укажите `deja embed` на локальную Ollama, LM Studio или любой OpenAI-совместимый эндпоинт через
`DEJA_EMBED_URL`, и вопрос другими словами тоже попадёт. Без доступного рантайма лексический поиск и
MCP-recall работают как обычно.

## Доказательства

```sh
deja bench recall     # нижняя граница регрессии ранжирования: 100 запросов, половина по-русски, CI падает при просадке
deja bench context    # 30 цепочек задач с сидами плюс пять негативных контролей
deja bench block      # остался ли ответ в том куске текста, который отдали
deja bench prompt     # когда пословный хук заговаривает и когда заговаривает зря
deja bench ingest     # цена одного обновления индекса: без изменений, +один ход, +один файл записи, полная перезапись файла
```

Контекстный эксперимент сравнивает recall deja с полной историей, наивным grep и холодным стартом. На сиде
по умолчанию:

| Подход | Медиана токенов | Медиана покрытия | Токены негативного контроля |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

То же покрытие фактов, что и у grep по сырым логам, при примерно в 250 раз меньшем числе токенов; примерно
в 70 раз меньше, чем у сессий, найденных полным проигрыванием; и ничего не подставляется в цепочки, для
которых подходящей истории нет. Генератор корпуса и разметка релевантности — обычный читаемый Go.
Прежде чем верить любым цифрам, разберитесь, как в них определено «релевантно» — включая наши.

Замерено на настоящем репозитории: 2,419 сессий, 179k сообщений, 1.9 ГБ записей.

| Метрика | Результат |
| --- | --- |
| Запрос внутри процесса | медиана **0.7–0.8 мс**, около 19 мс на стогах LongMemEval-S |
| `deja <запрос>` целиком | медиана около 0.2 с на этом репозитории: старт процесса, проверка свежести всех хранилищ, ранжирование, вывод |
| Только проверка свежести | около 50 мс, когда ничего не менялось |
| Размер индекса | 200 МБ, около 10% корпуса |

Индекс инкрементальный. Когда файл сессии дописан, перечитывается только он.

## Как это устроено

Локальный инвертированный индекс в `~/.cache/deja`: разбирает JSONL- и SQLite-хранилища, вычищает секреты,
пишет `records.bin` и словарные корзины и держит состояние каждого файла в `manifest.gob`, поэтому повторный
запуск забирает только изменившееся. MCP-сервер, статистика, share и sync читают этот же индекс. Подробности
в [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Частые вопросы

**Что-нибудь уходит с моей машины?** Нет, пока вы сами не попросите. См.
[потоки данных](../../docs/SECURITY-MODEL.md#data-flows).

**А секреты, которые уже лежат в логах?** Они остаются в файлах самих инструментов, это данные вашего
агента. В индекс deja, в выжимки, в share и в экспорт sync они не попадают.

**Это замедлит агента?** Один recall — лексический запрос к локальному индексу: медиана 0.7–0.8 мс, ничто
не ждёт модель. Хук добавляет старт процесса и проверку свежести хранилищ — десятки миллисекунд на
репозитории в несколько гигабайт.

**Придётся менять то, как я работаю?** Нет. Recall вызывает сам агент; с включённым авто-recall он уже на
старте сессии знает, что в этом проекте решали раньше.

**Чем это отличается от других инструментов памяти?**

| | deja | Платформы памяти<br>(Mem0, Letta, memU) | Поиск по сессиям<br>(cass) |
| --- | :-: | :-: | :-: |
| Знает работу, которая была до установки | да | нет | да |
| Нужен шаг сбора | нет, запись и есть память | агент или ваш код пишет факты | нет |
| Нужен ключ к LLM или эмбеддингам | нет | да | по желанию |
| Отдаёт контекст, когда его не спрашивали | на старте сессии и перед выполнением инструмента | нет | нет |

[Полное сравнение](https://vshulcz.github.io/deja-vu/guide/compare.html) охватывает одиннадцать из них.

**Где лежит история сессий Claude Code и можно ли по ней искать?** В `~/.claude/projects`, по одному
JSONL-файлу на сессию; Codex — в `~/.codex/sessions`, Cursor — в SQLite `state.vscdb`. `deja search` читает
их на месте, `deja last` показывает последнюю сессию каждого агента, `deja view` открывает всю историю одной
локальной страницей. Пути по агентам — в
[где хранятся сессии](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

**История сессий Claude Code пропала — она потеряна?** Claude Code удаляет записи старше 30 дней
(`cleanupPeriodDays` в `~/.claude/settings.json`), и `claude --resume` показывает только то, что осталось.
Сессии, которые deja проиндексировала до уборки, ищутся и после исчезновения файлов. Подробности в
[файлах сессий на диске](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

**Как удалить всё?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Руководства

Написаны по ситуациям, а не по функциям:

- [Помнит ли кодинг-агент прошлые разговоры?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — что каждый агент оставляет между сессиями, а что теряет
- [Файлы сессий на диске](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — насколько вырастает `~/.claude/projects` и чего стоит удаление
- [Агент потерял контекст](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — после падения, после очистки или когда сессия вернулась пустой
- [Контекстное окно заполнено](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — что на самом деле сохраняет компактация, с замерами, и чем это можно заменить
- [Продолжить вчерашнюю сессию](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — найти её по всем агентам и открыть в том, которому она принадлежит
- [Агент снова сделал ошибку, которую вы уже чинили](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Найти сессию, где это решили](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Почему агенты забывают между сессиями](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [где каждый агент хранит историю](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [Что теряет компактация](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [сменить агента](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [аудит того, что делали агенты](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [экспорт разговора](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [между машинами](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [во сколько токенов обходится память](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

По инструментам: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Попробуйте на своей истории

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Десять секунд на установку, десяток на индекс. В следующей сессии агент уже будет знать, что вы решали в
этом проекте — включая то, что было до установки deja.

## Разработка

`make build test lint`, дальше [CONTRIBUTING.md](../../CONTRIBUTING.md).
Новый инструмент начинается с [реестра парсеров](../../docs/ARCHITECTURE.md#source-parsers).
Приоритеты и то, чего мы делать не собираемся, — в [ROADMAP.md](../../ROADMAP.md).

## Лицензия

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
