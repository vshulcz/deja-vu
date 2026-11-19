<p align="center">
  <a href="https://github.com/vshulcz/deja-vu">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
      <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
    </picture>
  </a>
</p>

<p align="center"><b>Общая локальная память, передача задач между агентами и восстановление кода для 49 кодинг-агентов.</b></p>

<p align="center">Claude Code, Codex, Cursor, OpenClaw, Gemini CLI, OpenCode, Hermes и ещё 42 агента уже сохраняют каждую сессию на диск.<br>
deja индексирует всю историю локально, чтобы агенты сохраняли контекст после <code>/compact</code>, передавали задачу на полпути через <code>deja handoff</code> и восстанавливали перезаписанный код через <code>deja restore</code> — без LLM, один Go-бинарник.</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | Русский | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center">
  <a href="https://trendshift.io/repositories/83640" target="_blank"><img src="https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go" alt="vshulcz/deja-vu — Trendshift" width="250" height="55" /></a>
</p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg?branch=main&event=push" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://img.shields.io/badge/coverage-88%25-brightgreen" alt="Coverage"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases/latest"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://mcptoplist.com/server/io.github.vshulcz%2Fdeja-vu"><img src="https://mcptoplist.com/badge/io.github.vshulcz%2Fdeja-vu.svg" alt="MCP Toplist"></a>
  <a href="https://www.npmjs.com/package/@vshulcz/deja-vu"><img src="https://img.shields.io/npm/v/%40vshulcz%2Fdeja-vu" alt="npm"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
</p>

<p align="center"><a href="https://github.com/vshulcz/deja-vu"><b>GitHub</b></a> &middot; <a href="https://vshulcz.github.io/deja-vu/"><b>Сайт и интерактивный поиск</b></a> &middot; <a href="https://vshulcz.github.io/deja-vu/view/"><b>Живое демо в браузере</b></a> &middot; <a href="https://vshulcz.github.io/deja-vu/ru/guide/getting-started.html">Быстрый старт (RU)</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Замеры</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Сравнение</a></p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="один и тот же вопрос одному и тому же агенту дважды: без памяти он не помнит ничего, с deja отвечает выводом восьмимесячной давности"></p>

```sh
npx @vshulcz/deja-vu install --auto                # Установка в один шаг на любой ОС + подключение установленных агентов
# или через shell / Homebrew:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

<p align="center"><sub>Homebrew: <code>brew install deja-vu &amp;&amp; deja install --auto</code> &middot; Плагин Claude Code: <code>claude plugin marketplace add vshulcz/deja-vu &amp;&amp; claude plugin install deja-vu@deja-vu</code> &middot; Agent Skills: <code>npx skills add vshulcz/deja-vu</code><br><a href="#установка">Windows (Scoop / winget), Codex, OpenCode, OpenClaw и др.</a> &middot; Попробовать без установки: <code>npx @vshulcz/deja-vu stats --no-index --card</code> или <code>npx @vshulcz/deja-vu audit</code> &middot; <a href="https://vshulcz.github.io/deja-vu/view/">Демо в браузере →</a></sub></p>

<table align="center">
<tr>
<td align="center" width="33%">Полнота<br><b>97.2% R@5</b> на LongMemEval-S<br><sub>все 500 вопросов</sub></td>
<td align="center" width="33%">Токены<br><b>на 58% меньше токенов</b> на повторных задачах<br><sub>53,558 против 126,222 без памяти<br>477 токенов на описание инструментов, 0 вызовов LLM</sub></td>
<td align="center" width="33%">Индексация<br><b>13.6 с</b> на индекс 19,195 сессий<br><sub>26 мс до первого ответа, медиана поиска 97 мс</sub></td>
</tr>
</table>

<p align="center"><sub>Драйверы, синтетический корпус и правила подсчёта лежат в репозитории &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">сравнение лоб в лоб и как перепроверить любую строку</a> &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">прогоны LongMemEval и LoCoMo</a></sub></p>

## Установка

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Скрипт кладёт бинарник в `~/.local/bin`. Если этой папки ещё нет в `PATH`, вторая команда в той же оболочке не найдётся: запустите строку `next:`, которую печатает скрипт (в ней полный путь), или откройте новую оболочку, когда строка с `PATH` добавлена.

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
было решено. Чтобы поискать самому, запустите `deja`.

У opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw, pi и Hermes есть пакеты в их
собственных экосистемах — для тех, кто привык ставить расширения оттуда:

```sh
opencode plugin add opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: найдите deja в панели расширений
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add vshulcz/deja-vu && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja && openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
pi install npm:@vshulcz/pi-deja
hermes plugins install deja-memory
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
shell-скрипт; используйте `scoop install deja-vu` (есть в основном bucket Scoop) или `winget install vshulcz.deja-vu`, либо возьмите `deja-vu_<version>_windows_amd64.zip` из
[последнего релиза](https://github.com/vshulcz/deja-vu/releases/latest) и положите `deja.exe` в `PATH`.

Одного бинарника достаточно для полной работы: индексация, поиск, `show`, `ctx`, `blame`, `--json` и
вычистка секретов не требуют больше ничего. `deja install` подключает MCP к вашим агентам и включает recall
на старте сессии — это полезно, но не обязательно.

## Ищите сами

`deja` без аргументов в терминале открывает поиск по истории всех агентов. Сначала там сессии, стоящие
за вашими незакоммиченными изменениями, потом недавние в этом проекте, и список сужается по мере ввода.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/tui.gif" width="720" alt="В экране deja набирается запрос, найденная сессия открывается для чтения и продолжается в другом агенте"></p>

- `↑↓` выбирает сессию, `↵` открывает её на совпадении, `n` и `N` переходят между совпадениями, `esc` возвращает назад. Внутри сессии `/` ищет слово, `]` и `[` прыгают между вашими вопросами.
- Строка поиска понимает фильтры: агент с двоеточием (`codex:`), `today`, `yesterday`, `week`, `month` и `in:<проект>`.
- `r` возобновляет сессию в агенте, который её записал. `o` продолжает её в любом другом агенте, и тот начинает с того, что сессия спрашивала, решила и оставила открытым.
- `tab` переключает между этим проектом, всеми проектами и Deleted — сессиями, которые их агент удалил, а deja ещё хранит; `R` возвращает такую на место.
- `?` показывает все клавиши, в `^k` остальное (скопировать id сессии или путь проекта, забыть сессию), в списке `q` выходит; `esc` возвращает на шаг назад, а из пустой строки поиска выходит.

С `DEJA_TUI=0` вместо экрана печатается текстовая сводка, она же `deja brief`. В pipe печатается, что
проиндексировано и какую команду запустить дальше.

## Что это даёт

**Решено в Codex — помнит Claude.** Сорок девять кодинг-агентов пишут каждый разговор в локальные файлы,
deja превращает эти файлы в слой памяти, который читают все они.

| | |
| --- | --- |
| **Поиск назад во времени** | `deja "connection pool exhausted"` ищет по гигабайтам, включая всё, что было до установки deja. Вопрос на естественном языке откатывается к режиму релевантности. Время — подсказка, а не фильтр. |
| **Recall между агентами** | MCP-инструмент `deja` в режиме `recall` в любом агенте отвечает «это мы чинили три недели назад», кто бы тогда ни чинил. |
| **Переживает компактацию** | Замерено на 43 компактациях: сводка сохранила 77% решений и 0.2% команд, которые вы запускали. Остальные 99.8% возвращает deja. В Claude Code и Codex deja записывает задачу, файлы и команды в момент начала компактации и один раз отдаёт их после неё. |
| **Recall в момент действия** | Перед тем как агент правит файл или запускает команду, хук `PreToolUse` называет прежние решения по этому файлу, рабочий вариант этой команды или программу, которой на этой машине просто нет. Когда команда падает, хук `PostToolUse` показывает, что запускали после той же ошибки на этой машине — ровно та пара, про которую агент сам не спросит. |
| **Индексируется работа, а не только слова** | Каждый открытый за ход файл, каждая запущенная команда с кодом возврата и точный фрагмент, который заменила правка. Ровно то, что теряет любая сводка. |

Ещё: `deja promote <id> --state rejected` помечает отменённое решение, и дальше каждое попадание показывает,
что это пробовали и отвергли; попадание сообщает «4 файла этой сессии с тех пор изменились» и молчит, когда
судить не может; `deja sync ssh laptop` переносит память между машинами только дописыванием, без облака
посередине; `deja handoff --to codex` упаковывает текущий контекст, чтобы продолжить в другом агенте;
ключи, токены, JWT и блоки приватных ключей известных форм вырезаются при индексации — но совпадение по
шаблону не детектор секретов, незнакомая форма может пройти.

Полный справочник — на [сайте документации](https://vshulcz.github.io/deja-vu/).

## Память в репозитории

Синхронизация переносит историю между вашими машинами. Чтобы передать коллегам те немногие
вещи, которые ваши агенты усвоили на собственных ошибках, положите их в репозиторий:

```text
$ deja repo candidates
4cd7e7bb9c rejected Reverted the retry with jitter: it broke idempotency because the sender re-signs each attempt with a new delivery id.  (session s1, claude 2026-08-03)
$ deja repo add 4cd7e7bb9c
added 1 entry to .deja/memory.jsonl
nothing is committed: review with `git diff .deja/` and commit it like code
```

Кандидаты собираются из истории этого проекта во всех агентах: закреплённые и запомненные
заметки, откаты, о которых сообщалось в сессиях, и принятые решения. Ничего не записывается,
пока вы сами не выберете; при записи каждая запись проходит редактирование секретов, и если
в ней всё ещё срабатывает правило секретов, она отклоняется. Файл `.deja/memory.jsonl` — это
JSON Lines, отсортированные по хэшу содержимого, с атрибутом `merge=union` рядом, поэтому
параллельные добавления сливаются без конфликтов; удаление строки удаляет запись. После клонирования
записи возвращаются при старте сессии и в recall в любом подключённом к deja агенте с пометкой,
что они из репозитория, и в том же недоверенном обрамлении, что и остальная память.
`{"activations":{"auto":{"repo":false}}}` в политике доверия исключает их из хуков.
[Подробнее](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

## Приватность

Индексация и поиск локальны. Сеть нужна только `deja update`, `deja sync ssh`, проверке версии внутри
`deja doctor`, `deja embed`, который ходит на заданный вами эндпоинт (а когда семантический сайдкар собран, ещё и каждый поиск: он отправляет запрос на этот эндпоинт), и раз в день интерактивной команде,
чтобы узнать последний релиз. Эта проверка не отправляет данных сессий; `DEJA_OFFLINE=1` или
`DEJA_NO_UPDATE_NOTICE=1` её отключает.

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

Голый `deja` в терминале открывает [экран поиска](#ищите-сами). С запросом или любой командой ниже он
печатает результат и завершается:

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
| `deja` | В терминале — экран поиска, описанный выше. `deja brief` вместо него печатает текстовую сводку: сегодняшние сессии, что отдала deja, недавняя работа и запрос, который стоит попробовать. |
| `deja wip` | Чем занималась прошлая сессия в этом каталоге: задача, к чему пришли, открытые файлы, последняя команда и упала ли она. Всё выведено из записей, без опоры на чьи-то заметки. |
| `deja blame <путь>[:строка]` | Какие сессии обсуждали этот файл, что решили и почему. Со строкой: коммит, изменивший её последним, и сессия, которая написала эту строку или текст, заменённый этим коммитом. |
| `deja files <тема>` | В обратную сторону: какие файлы на самом деле трогала работа по теме. |
| `deja how <инструмент>` | Как эту вещь реально запускают на этой машине, с настоящими аргументами, из команд, которые агенты уже выполняли. |
| `deja fix <ошибка>` | Что запускали после той же ошибки на этой машине, и после чего она больше не появлялась. Никогда не merge, force push или удаление. |
| `deja friction` | Ошибки, попавшие в три и больше разных сессий, с указанием, из каких они инструментов. |
| `deja ctx <запрос>` | Markdown-выжимка лучшего попадания вместе с кодом, который та сессия изменила, годная прямо в промпт. |
| `deja resume <id>` | Открывает найденную сессию заново в её родном инструменте. |
| `deja view` | Выгружает всю память в один локальный HTML-файл. Без сервера, ничего не уходит с машины. |
| `deja doctor [--deep]` | Самопроверка; с `--deep` сверяет индекс с исходными файлами. |
| `deja mcp` | MCP-сервер по stdio — тот самый, который подключает `deja install`. |

Полный справочник — в [документации команд](https://vshulcz.github.io/deja-vu/guide/commands.html).

### MCP-инструменты

Сервер отдаёт один инструмент `deja`, режим выбирается параметром `mode`: `recall`, `context`, `blame`,
`fix`, `how`, `orient`, `remember`, `handoff`. `deja install` подключает его сам — это важно знать только при ручной настройке
агента. Прежние шесть имён инструментов продолжают работать у уже подключённых клиентов.

Один инструмент вместо семи — это про стоимость, а не про стиль. Подключённый MCP-сервер отправляет описания
своих инструментов с каждым запросом, так что за них платят каждый ход независимо от того, вызвал агент
что-нибудь или нет: здесь 477 токенов против 8,283 у самого крупного из семи измеренных серверов. У самой
deja было 828, пока схему не свели к одному инструменту с режимами.

## Поддерживаемые инструменты

С включённым авто-recall Claude Code и Codex отдают deja текущую запись в момент начала компактации, и она
сохраняет то, что сводка вот-вот потеряет: задачу, выводы, файлы, чем закончилась каждая команда и что
осталось недоделанным. Следующий хук в той же сессии и том же рабочем каталоге возвращает это одним куском,
не больше 4 КБ, со строкой о том, менялся ли репозиторий с тех пор. `deja stats` считает количество вызовов
инструментов между компактацией и первой правкой — этим и меряли.
Подробности во [восстановлении после компактации](../../docs/compaction.md).

Claude Code · Claw Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Reasonix · Kiro · Kilo Code · CodeBuddy Code ·
CodeArts Desktop ·
Qoder · TRAE CLI · Muse Code · Junie · JetBrains AI Assistant · Devin CLI · Windsurf / Devin Desktop · MiniMax Code · MiMo Code · Omnigent · Orca · Zed.

Что именно каждый из них поддерживает — MCP-recall, авто-recall, skills, команды, resume, handoff — см. в
[матрице возможностей в английском README](../../README.md#supported-harnesses). Нестандартные пути к хранилищам
задаются переменными `DEJA_*_ROOT`, переменные миграции самих инструментов тоже учитываются.

### Агенты со своим пакетом

`deja install --auto` подключает эти так же, как остальные, и это всегда самый короткий путь. Заодно у
каждого есть пакет в своей экосистеме — для тех, кто ставит расширения оттуда:

| Агент | Пакет | Установка |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin add opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | плагин `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Gemini CLI | расширение `@vshulcz/deja`, в [Gemini CLI Extensions Gallery](https://geminicli.com/extensions/) | `gemini extensions install https://github.com/vshulcz/deja-vu` |
| Antigravity | плагин `deja-vu` ([`antigravity-plugin/`](../../antigravity-plugin/)) | `agy plugin install deja-vu` |
| Codex CLI | плагин `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, затем `codex plugin add deja-vu@deja-vu` |
| Grok Build | плагин `deja` | `grok plugin marketplace add vshulcz/deja-vu`, затем `grok plugin install deja` |
| OpenClaw | ClawHub и npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (и omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |
| Hermes | провайдер памяти `deja-memory` в каталоге плагинов Hermes | `hermes plugins install deja-memory` или с GitHub: `hermes plugins install vshulcz/deja-vu/extensions/hermes` |
| Magpie | npm `@vshulcz/magpie-deja` | `magpie plugin add @vshulcz/magpie-deja` |
| Orca | v2-плагин `trusted-worker` `deja` | `Orca → Plugins → extensions/orca` (и `deja install orca` для нативного MCP и хуков) |
| CodeWhale | Claude-совместимый набор `deja-vu` | `/plugin install extensions/codewhale` (и `deja install codewhale` для нативных хуков) |
| VS Code / Cursor / Windsurf | расширение строки состояния `vshulcz.deja-status` | `code --install-extension vshulcz.deja-status` |
| JetBrains IDEs | [JetBrains Marketplace](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory) (`com.github.vshulcz.deja-vu`) ([`extensions/jetbrains/`](../../extensions/jetbrains/docs/ru.md)) | `idea installPlugins com.github.vshulcz.deja-vu` |
| Neovim | плагин для `lazy.nvim` и CodeCompanion ([`extensions/nvim/`](../../extensions/nvim/docs/ru.md)) | `{ "vshulcz/deja-vu", opts = {} }` |
| tmux | всплывающее окно [TPM](https://github.com/tmux-plugins/tpm) (`prefix + D`) и `#{deja_status}` (`deja.tmux`) | `set -g @plugin 'vshulcz/deja-vu'` |
| Raycast | расширение Raycast `deja-vu` | `cd extensions/raycast && npm install && npx ray develop` |
| CC Switch | импорт MCP и репозитория Skills в 1 клик (`ccswitch://`) | [Добавить MCP в CC Switch](ccswitch://v1/import?resource=mcp&apps=claude,codex,gemini,opencode&config=eyJtY3BTZXJ2ZXJzIjp7ImRlamEiOnsiY29tbWFuZCI6Im5weCIsImFyZ3MiOlsiLXkiLCJAdnNodWxjei9kZWphLXZ1Iiwic2VydmUiXX19fQ==) · [Переход между агентами](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html) |

Любого пути достаточно, оба вместе тоже не мешают: каждый пакет сначала читает то, что записал
`deja install`. opencode, dsh и OpenClaw дописывают только недостающее; Kimi, Grok, Codex и pi уступают,
если установщик уже всё подключил; в Zed обе стороны используют один и тот же id сервера. Так что порядок
установки значения не имеет.

Все они используют уже установленный вами deja, своя копия внутри пакета — только запасной вариант.

В проекте на [Spec Kit](https://github.com/github/spec-kit) расширение добавляет команду
`/speckit.deja-vu.recall`, которая ищет по прошлым сессиям контекст фичи до начала реализации:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

## Семантический recall, по желанию

Укажите `deja embed` на локальную Ollama, LM Studio или любой OpenAI-совместимый эндпоинт через
`DEJA_EMBED_URL`, и вопрос другими словами тоже попадёт. Без доступного рантайма лексический поиск и
MCP-recall работают как обычно.

## Доказательства

<p>
<b>На 58% меньше токенов</b> на задаче, которую эта машина уже решала &middot; <b>88.9% hit@1</b> на LongMemEval-S (очищенный набор из 470 вопросов) &middot; <b>71.9%</b> на LoCoMo &middot; поиск по гигабайтам истории за <b>миллисекунды</b><br>
<sub>По 11 прогонов на плечо: 53,558 токенов против 126,222 без всякой памяти; на более поздней сборке, снова по 11 прогонов: 52,815 против 103,443 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">во что обходится одна задача</a> &middot;
оба стенда для оценки поиска лежат в этом репозитории и прогоняются на публичных датасетах за минуты &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">проверьте цифры сами</a></sub>
</p>

```sh
deja bench recall     # нижняя граница регрессии ранжирования: 100 запросов, половина по-русски, CI падает при просадке
deja bench context    # 30 цепочек задач с сидами плюс пять негативных контролей
deja bench block      # остался ли ответ в том куске текста, который отдали
deja bench prompt     # когда пословный хук заговаривает и когда заговаривает зря
deja bench ingest     # цена одного обновления индекса: без изменений, +один ход, +один файл записи, полная перезапись файла
deja bench read       # сколько стоит прочитать хранилище в базе данных и что с ним делает одно длинное значение
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

Замерено на настоящем хранилище: 2,419 сессий, 179k сообщений, 1.9 ГБ записей.

| Метрика | Результат |
| --- | --- |
| Запрос внутри процесса | медиана **0.7–0.8 мс**, около 15 мс на стогах LongMemEval-S |
| `deja <запрос>` целиком | медиана около 0.2 с на этом хранилище: старт процесса, проверка свежести всех хранилищ, ранжирование, вывод |
| Только проверка свежести | около 50 мс, когда ничего не менялось |
| Размер индекса | 200 МБ, около 10% корпуса |

Индекс инкрементальный. Когда файл сессии дописан, перечитывается только он.

## Как это устроено

Локальный инвертированный индекс в `~/.cache/deja`: разбирает JSONL- и SQLite-хранилища, вычищает секреты,
пишет `records.bin` и словарные корзины и держит состояние каждого файла в `manifest.gob`, поэтому повторный
запуск забирает только изменившееся. MCP-сервер, статистика, share и sync читают этот же индекс. Подробности
в [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md).

## Частые вопросы

<details>
<summary><b>Чем это отличается от других инструментов памяти?</b></summary>

| | deja | Платформы памяти | Поиск по сессиям |
| --- | :-: | :-: | :-: |
| Знает работу, которая была до установки | да | нет | да |
| Нужен шаг сбора | нет, запись и есть память | агент или ваш код пишет факты | нет |
| Нужен ключ к LLM или эмбеддингам | нет | да | по желанию |
| Отдаёт контекст, когда его не спрашивали | на старте сессии и перед выполнением инструмента | нет | нет |

[Полное сравнение](https://vshulcz.github.io/deja-vu/guide/compare.html) охватывает пятнадцать из них.

</details>

<details>
<summary><b>Это замедлит агента?</b></summary>

Один recall — лексический запрос к локальному индексу: медиана 0.7–0.8 мс, ничто
не ждёт модель. Хук добавляет старт процесса и проверку свежести хранилищ — на индексе из 19k сессий хук на каждый промпт занимает 107 мс по медиане.

</details>

<details>
<summary><b>Где лежит история сессий Claude Code и можно ли по ней искать?</b></summary>

В `~/.claude/projects`, по одному
JSONL-файлу на сессию; Codex — в `~/.codex/sessions`, Cursor — в SQLite `state.vscdb`. deja читает
их на месте: голый `deja` открывает экран, где их можно искать и читать, `deja search` делает то же из скрипта, `deja last` показывает последние сессии всех агентов, `deja view` открывает всю историю одной
локальной страницей. Пути по агентам — в
[где хранятся сессии](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

</details>

<details>
<summary><b>История сессий Claude Code пропала — она потеряна?</b></summary>

Claude Code удаляет записи старше 30 дней
(`cleanupPeriodDays` в `~/.claude/settings.json`), и `claude --resume` показывает только то, что осталось.
Сессии, которые deja проиндексировала до уборки, ищутся и после исчезновения файлов. Подробности в
[файлах сессий на диске](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

</details>

<details>
<summary><b>Как удалить всё?</b></summary>

```sh
deja uninstall --all
rm -rf ~/.cache/deja ~/.local/share/deja ~/.config/deja
```

Индекс лежит в `~/.cache/deja`, заметки `remember` и `promote` в `~/.local/share/deja`,
правила, список исключений и запускатель хуков в `~/.config/deja` (или под
`XDG_DATA_HOME` и `XDG_CONFIG_HOME`, если они заданы; на Windows заметки в
`%AppData%\deja`).

</details>

## Руководства

- **Контекст и восстановление:** [Помнит ли кодинг-агент прошлые разговоры?](https://vshulcz.github.io/deja-vu/ru/guide/does-my-agent-remember.html) · [Агент потерял контекст](https://vshulcz.github.io/deja-vu/guide/lost-context.html) · [Контекстное окно заполнено](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) · [Что теряет компактация](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [Почему агенты забывают между сессиями](https://vshulcz.github.io/deja-vu/guide/forgetting.html)
- **Поиск и продолжение работы:** [Продолжить вчерашнюю сессию](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) · [Найти сессию, где это решили](https://vshulcz.github.io/deja-vu/guide/find-a-session.html) · [Агент снова сделал ошибку, которую вы уже чинили](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html) · [Сменить агента](https://vshulcz.github.io/deja-vu/guide/switching-agents.html)
- **Файлы сессий и очистка:** [Файлы сессий на диске](https://vshulcz.github.io/deja-vu/ru/guide/session-files-on-disk.html) · [Где каждый агент хранит историю](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html) · [state.vscdb в Cursor слишком большой](https://vshulcz.github.io/deja-vu/ru/guide/cursor-state-vscdb-too-large.html) · [История сессий Codex CLI](https://vshulcz.github.io/deja-vu/ru/guide/codex-history.html)
- **Эксплуатация и стоимость:** [Аудит того, что делали агенты](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [Экспорт разговора](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [Между машинами](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [Во сколько токенов обходится память](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

## Разработка

`make build test lint`, дальше [CONTRIBUTING.md](../../CONTRIBUTING.md).
Новый инструмент начинается с [реестра парсеров](../../docs/ARCHITECTURE.md#source-parsers).
Приоритеты и то, чего мы делать не собираемся, — в [ROADMAP.md](../../ROADMAP.md).

## Поддержка

Ошибки и вопросы направляйте в [issues](https://github.com/vshulcz/deja-vu/issues).
Всё, что может быть уязвимостью, отправляйте через приватную ссылку на уведомление в
[SECURITY.md](../../SECURITY.md). Что deja читает, чего он никогда никуда не отправляет и как
исключить проект или забыть сессию, описано в разделе [Приватность](#приватность).

## Лицензия

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
