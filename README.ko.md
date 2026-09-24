<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>모든 코딩 에이전트가 함께 쓰는 하나의 기억. 이미 디스크에 쌓인 기록에서 만들어집니다.</b></p>

<p align="center">에이전트가 지금 막 3월에 고친 문제를 다시 디버깅하려 합니다. 그때는 다른 에이전트였죠.
deja는 Claude Code, Codex, Cursor를 비롯해 이 컴퓨터의 모든 에이전트가 이미 디스크에 쓰고 있는 세션을
색인하고, 어느 에이전트가 묻든 맞는 기록을 돌려줍니다.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="같은 에이전트에게 같은 질문을 두 번: 기억이 없을 때는 아무것도 모르고, deja가 있으면 8개월 전 결론으로 답한다"></p>

<p align="center"><sub><em>아무도 검색하지 않았습니다. 에이전트가 스스로 deja를 호출했습니다. 실제 모델과 실제 도구 호출로 진행한 두 번의 실제 실행이며, 합성 코퍼스 위에서 돌렸기 때문에 누구의 기록도 공개되지 않습니다.</em></sub></p>

<p align="center"><b>deja는 처음부터 가득 차 있습니다. 34개 에이전트가 이미 남긴 기록, 몇 초 만에 끝나는 색인, 모델도 별도의 수집 단계도 없습니다.</b></p>

<p align="center">
이 컴퓨터가 이미 해결한 작업에서 <b>토큰 58% 절감</b> &middot; LongMemEval-S(470문항 정제 세트)에서 <b>hit@1 88.1%</b> &middot; LoCoMo에서 <b>70.5%</b> &middot; 수 기가바이트 기록을 <b>밀리초</b> 단위로 조회<br>
<sub>한쪽당 11회 실행: 53,558 토큰 대 아무것도 연결하지 않았을 때의 126,222, 같은 스탠드의 이후 실행에서는 71% &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">작업 하나를 끝내는 비용</a> &middot;
검색 평가 하네스 두 개 모두 이 저장소에 있고 공개 데이터셋에서 몇 분이면 돕니다 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">숫자를 직접 확인하세요</a></sub>
</p>

<p align="center"><a href="README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | 한국어 | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">문서</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">벤치마크</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">비교</a></p>
<p align="center"><sub>쓸 만하다면 <a href="https://github.com/vshulcz/deja-vu">GitHub</a>에서 deja-vu에 별을 눌러 주세요.</sub></p>

## 설치

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

`raw.githubusercontent.com`에 연결되지 않으면 npm 미러를 쓰세요. 버전은 같습니다:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

설치에 10초, 색인에 10초쯤 걸립니다. 두 번째 명령은 찾아낸 모든 에이전트에 MCP recall을 연결하고,
지원하는 곳에서는 세션 시작 시 회상을 켜고, 첫 색인을 만들어 둡니다. 다음 세션이 기다릴 일이 없도록.

새 에이전트 세션을 열고 몇 달 전에 했던 일을 물어보세요:

> jwt refresh rotation 전에 다뤄본 적 있나? 기억을 찾아봐

굳이 묻지 않아도 됩니다. 자동 회상을 켜 두면 세션이 열리는 순간 에이전트는 이 프로젝트에서 무엇을
해결했는지 이미 알고 있습니다.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw, pi는 각자의 생태계에
패키지가 있습니다. 확장을 그쪽에서 설치하는 데 익숙하다면:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: 확장 패널에서 deja 검색
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto`가 위의 것들을 알아서 연결하므로 두 경로 중 하나면 충분합니다. 둘 다 써도
문제없습니다. 각 패키지는 `deja install`이 써 둔 것을 읽고 빠진 부분만 채우므로, 도구가 중복 등록되거나
회상이 두 번 일어나지 않습니다. 자세한 내용은 [`extensions/`](extensions)에 있습니다.

같은 검색이 skill로도 존재해서, `SKILL.md`를 읽는 에이전트라면 어디든 설치할 수 있습니다:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

skill은 이미 설치된 `deja` 바이너리를 호출하며, 자체 바이너리를 들고 다니지 않습니다.

다른 설치 방법: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, 또는 아무것도 설치하지 않고 먼저 써 보려면
`npx @vshulcz/deja-vu "검색어"`. Windows에서는 설치 스크립트가 `unsupported OS`로 끝납니다. shell
스크립트이기 때문입니다. [최신 릴리스](https://github.com/vshulcz/deja-vu/releases/latest)에서
`deja-vu_<version>_windows_amd64.zip`을 받아 `deja.exe`를 `PATH`에 두세요.

바이너리 하나만으로도 온전한 설치입니다. 색인, 검색, `show`, `ctx`, `blame`, `--json`, 비밀정보 제거는
다른 무엇도 필요로 하지 않습니다. `deja install`이 하는 일은 MCP를 에이전트에 연결하고 세션 시작 회상을
켜는 것입니다. 있으면 좋지만 선택 사항입니다.

## 무엇을 얻나

**Codex에서 해결하면 Claude가 기억합니다.** 서른네 개의 코딩 에이전트가 모든 대화를 로컬 파일에 쓰고,
deja는 그 파일들을 모두가 읽을 수 있는 하나의 기억 계층으로 바꿉니다.

| | |
| --- | --- |
| **과거로 향하는 검색** | `deja "connection pool exhausted"`는 deja를 설치하기 이전 것까지 포함해 수 기가바이트를 훑습니다. 자연어 질문은 관련도 모드로 넘어갑니다. 시간은 힌트이지 필터가 아닙니다. |
| **에이전트를 넘는 회상** | MCP의 `deja` 도구는 `recall` 모드로, 어느 에이전트에서든 "이건 3주 전에 고쳤다"고 답합니다. 그때 누가 고쳤든 상관없이. |
| **압축 이후에도 남습니다** | 43번의 컨텍스트 압축에서 측정: 요약은 결정의 77%와 실행한 명령의 0.2%를 지켰습니다. 나머지 99.8%는 deja가 돌려줍니다. Claude Code와 Codex에서는 압축이 시작되는 순간 deja가 작업, 파일, 명령을 적어 두고 다음 세션에서 한 번에 돌려줍니다. |
| **행동하는 바로 그 순간의 회상** | 에이전트가 파일을 고치거나 명령을 실행하기 직전에 `PreToolUse` 훅이 이 파일에 대한 이전 결정, 이 명령이 실제로 통하는 형태, 또는 이 컴퓨터에 아예 없는 프로그램을 말해 줍니다. 명령이 실패하면 `PostToolUse` 훅이 이 컴퓨터에서 같은 오류 뒤에 무엇을 실행했는지 보여 줍니다. 에이전트가 스스로 묻지 않는 바로 그 한 쌍입니다. |
| **말이 아니라 작업을 색인합니다** | 각 턴에서 연 파일, 실행한 명령과 종료 코드, 편집이 대체한 정확한 조각. 어떤 요약이든 놓치는 부분이 바로 그것입니다. |

그 밖에: `deja promote <id> --state rejected`는 뒤집힌 결정을 표시하고, 이후 모든 검색 결과에 시도했다가
기각되었음이 함께 나옵니다. 검색 결과는 "이 세션이 다룬 파일 4개가 그 뒤 변경됨"을 알리고, 판단할 수 없을
때는 말하지 않습니다. `deja sync ssh laptop`은 기계 사이에서 기억을 옮기며, 추가만 할 뿐 중간에 클라우드가
없습니다. `deja handoff --to codex`는 현재 컨텍스트를 묶어 다른 에이전트에서 이어가게 합니다. 알려진 형태의
키, 토큰, JWT, 개인 키 블록은 색인 시점에 제거됩니다. 다만 패턴 매칭은 비밀정보 탐지기가 아니라서 모르는
형태는 지나갈 수 있습니다.

### 자기 작업을 그려 보기

`deja stats --card`는 터미널에 바로 그립니다. 파일 이름을 주면 프로필 README에 넣을 SVG를 씁니다.
다른 곳에 올리려면 [PNG로 변환](https://vshulcz.github.io/deja-vu/card/)하세요. 변환은 여러분 브라우저
안에서 일어납니다.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja 통계 카드: 1년치 세션 히트맵, 어느 에이전트에서 왔는지, 가장 길었던 세션"></p>

전체 기능 레퍼런스는 [문서 사이트](https://vshulcz.github.io/deja-vu/)에 있습니다.

## 프라이버시

색인과 검색은 로컬에서 일어납니다. 네트워크를 쓰는 것은 `deja update`, `deja sync ssh`,
`deja doctor` 안의 버전 확인, 그리고 여러분이 지정한 엔드포인트로 가는 `deja embed`뿐입니다.

자격 증명은 색인 시점에 제거됩니다. AWS 키, `api_key=`와 `token=` 대입, bearer 토큰과 맨 JWT,
PEM 개인 키 블록, 각 제공자의 토큰, `scheme://user:pass@host` 형태의 URL, 어떤 규칙에도 걸리지 않는
고엔트로피 값, 그리고 산문 속에 쓰인 비밀번호("the admin password is …"처럼 기댈 구분자가 없는 경우)까지.
값은 `[redacted:<kind>]`가 되고 주변 텍스트는 그대로 검색됩니다. `deja share`와 `deja sync export`는
내보낼 때 한 번 더 제거합니다. 패턴 매칭은 비밀정보 탐지기가 아닙니다. 규칙이 모르는 형태는 그대로 지나갈 수
있습니다. 보안 모델을 참고하세요.

`deja forget`은 세션을 재구축된 색인에서 빼고 묘비를 남기므로, 이후의 `deja index`가 원본 기록에서
되살리지 못합니다.
[보안 모델](docs/SECURITY-MODEL.md)에 데이터 흐름, 제거의 경계, 신뢰 가정, 릴리스 검증이 적혀 있습니다.

## 명령줄

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| 명령 | 하는 일 |
| --- | --- |
| `deja <검색어>` | 전체 기록을 검색합니다. 여러 단어는 AND이고 따옴표는 연속된 문구를 요구하며, 정확히 맞는 것이 없으면 어형과 비슷한 철자를 시도합니다. |
| `deja wip` | 이 디렉터리의 지난 세션이 무엇을 하고 있었는지: 작업, 결론, 손에 들고 있던 파일, 마지막 명령과 그것이 실패했는지. 전부 기록에서 도출하며 누가 메모를 남겼는지에 기대지 않습니다. |
| `deja blame <경로>[:줄]` | 어떤 세션이 이 파일을 논의했고 무엇을 왜 결정했는지. 줄 번호를 주면 그 줄을 마지막으로 바꾼 커밋과, 그 커밋이 지운 텍스트를 쓴 세션을 보여 줍니다. |
| `deja files <주제>` | 반대 방향: 어떤 주제의 작업이 실제로 건드린 파일들. |
| `deja how <도구>` | 이 컴퓨터에서 그 일을 실제로 어떻게 실행하는지, 에이전트가 이전에 돌린 명령에서 가져온 진짜 인자와 함께. |
| `deja fix <오류>` | 이 컴퓨터에서 같은 오류 뒤에 무엇을 실행했고, 그 뒤 오류가 다시 나타나지 않았는지. |
| `deja friction` | 서로 다른 세션 셋 이상에서 나온 오류와, 어느 도구에서 왔는지. |
| `deja ctx <검색어>` | 최상위 결과의 Markdown 요약. 프롬프트에 바로 넣을 수 있습니다. |
| `deja resume <id>` | 찾은 세션을 원래의 도구에서 다시 엽니다. |
| `deja view` | 기억 전체를 로컬 HTML 파일 하나로 내보냅니다. 서버가 없고 데이터는 기계를 떠나지 않습니다. |
| `deja doctor [--deep]` | 자가 점검. `--deep`이면 원본 파일로 색인을 검증합니다. |
| `deja mcp` | stdio MCP 서버. `deja install`이 연결하는 바로 그것입니다. |

전체 레퍼런스는 [명령 문서](https://vshulcz.github.io/deja-vu/guide/commands.html)에 있습니다.

### MCP 도구

서버는 `deja` 하나만 노출하고 `mode` 인자로 기능을 고릅니다: `recall`, `context`, `blame`, `fix`,
`how`, `remember`. `deja install`이 알아서 연결하므로 에이전트를 수동으로 설정할 때만 신경 쓰면 됩니다.
이전의 여섯 도구 이름은 이미 연결된 클라이언트에서 계속 동작합니다.

일곱이 아니라 하나인 것은 취향이 아니라 비용 문제입니다. 연결된 MCP 서버는 요청마다 도구 정의를 함께
보내므로, 에이전트가 무엇을 호출했든 안 했든 매 턴 그 값을 냅니다. 여기서는 477 토큰이고, 측정한 여덟 개
서버 중 가장 큰 것은 8,283입니다. deja 자신도 스키마를 모드가 있는 하나의 도구로 줄이기 전에는 828이었습니다.

## 지원하는 도구

자동 회상을 켜면 Claude Code와 Codex는 압축이 시작될 때 현재 기록을 deja에 넘기고, deja는 요약이 곧 잃을
것을 남깁니다. 작업, 결론, 파일, 각 명령이 어떻게 끝났는지, 무엇이 아직 남았는지. 같은 세션, 같은 작업
디렉터리의 다음 훅이 그것을 4 KB를 넘지 않는 한 덩어리로 돌려주고, 저장소가 그 뒤 바뀌었는지 한 줄로
덧붙입니다. `deja stats`는 압축과 첫 편집 사이의 도구 호출 수를 세는데, 이 기능은 그것으로 측정했습니다.
자세한 내용은 [압축 이후 자동 복구](docs/compaction.md)에 있습니다.

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

각 도구가 MCP 회상, 자동 회상, skill, 명령, resume, handoff 중 무엇을 지원하는지는
[영문 README의 기능 표](README.md#supported-harnesses)에 있습니다. 저장 위치를 바꿨다면 `DEJA_*_ROOT`
변수로 지정하고, 각 도구 자체의 마이그레이션 변수도 존중합니다.

### 자체 패키지가 있는 에이전트

`deja install --auto`는 아래 것들도 다른 도구와 똑같이 연결하며, 그게 언제나 가장 짧은 길입니다.
동시에 각자의 생태계에 패키지가 있어서, 확장을 그쪽에서 설치하는 사람에게 편합니다:

| 에이전트 | 패키지 | 설치 |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | 플러그인 `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | 플러그인 `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu` 다음 `codex plugin add deja-vu@deja-vu` |
| Grok Build | 플러그인 `deja` | `grok plugin marketplace add xai-org/plugin-marketplace` 다음 `grok plugin install deja` |
| OpenClaw | ClawHub와 npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (그리고 omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

어느 쪽이든 하나면 충분하고 둘 다 해도 탈이 없습니다. 각 패키지는 먼저 `deja install`이 써 둔 설정을
읽습니다. opencode, dsh, OpenClaw는 빠진 부분만 채우고, Kimi, Grok, Codex, pi는 설치기가 이미 연결했다면
물러섭니다. Zed에서는 양쪽이 같은 server id를 씁니다. 그래서 설치 순서는 상관없습니다.

모두 여러분이 이미 설치한 deja를 사용하며, 패키지 안에 든 것은 대비책일 뿐입니다.

## 선택적인 의미 기반 회상

`DEJA_EMBED_URL`로 `deja embed`를 로컬 Ollama, LM Studio 또는 OpenAI 호환 엔드포인트에 가리키면
다른 표현으로 물어도 걸립니다. 쓸 수 있는 런타임이 없으면 어휘 검색과 MCP 회상은 평소대로 동작합니다.

## 근거

```sh
deja bench recall     # 랭킹 회귀의 하한: 질의 100개, 절반은 러시아어, 회상이 떨어지면 CI 실패
deja bench context    # 시드가 있는 작업 사슬 30개와 음성 대조군 5개
deja bench block      # 넘겨준 텍스트 조각에 답이 남아 있는지
deja bench prompt     # 프롬프트별 훅이 언제 말하고 언제 잘못 말하는지
deja bench ingest     # 색인 갱신 한 번의 비용: 변화 없음, 한 턴 추가, 기록 파일 하나 추가, 파일 전체 재작성
```

컨텍스트 실험은 deja 회상을 전체 기록, 단순 grep, 콜드 스타트와 비교합니다. 기본 시드에서:

| 방식 | 토큰 중앙값 | 커버리지 중앙값 | 음성 대조군 토큰 |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

원본 로그를 직접 grep할 때와 같은 사실 커버리지를 약 250배 적은 토큰으로 냅니다. 전체 재생으로 찾은
세션보다 약 70배 적고, 관련 기록이 없는 사슬에는 아무것도 주입하지 않습니다. 코퍼스 생성기와 관련도
라벨링은 평범하게 읽을 수 있는 Go 코드입니다. 어떤 숫자든 믿기 전에 거기서 "관련 있음"을 어떻게 정의했는지
먼저 보세요. 우리 숫자도 포함해서.

실제 저장소에서 측정: 세션 2,419개, 메시지 179k개, 기록 1.9 GB.

| 지표 | 결과 |
| --- | --- |
| 프로세스 안 질의 | 중앙값 **0.7–0.8 ms**, LongMemEval-S 건초더미에서는 약 19 ms |
| `deja <검색어>` 전 구간 | 이 저장소에서 중앙값 약 0.2 s: 프로세스 시작, 모든 저장소 신선도 확인, 랭킹, 출력 |
| 신선도 확인만 | 변화가 없을 때 약 50 ms |
| 색인 크기 | 200 MB, 코퍼스의 약 10% |

색인은 증분입니다. 세션 파일이 길어지면 그 파일 하나만 다시 읽습니다.

## 작동 방식

`~/.cache/deja`에 있는 로컬 역색인입니다. JSONL과 SQLite 저장소를 파싱하고, 자격 증명을 제거하고,
`records.bin`과 단어 버킷을 쓰고, 각 파일의 상태를 `manifest.gob`에 기록하므로 다시 실행하면 바뀐 것만
가져옵니다. MCP 서버, 통계, share, sync가 모두 같은 색인을 읽습니다. 자세한 내용은
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)에 있습니다.

## 자주 묻는 질문

**뭔가 내 컴퓨터를 떠나나요?** 직접 요청하지 않는 한 아닙니다.
[데이터 흐름](docs/SECURITY-MODEL.md#data-flows)을 보세요.

**이미 로그에 있는 비밀정보는요?** 그건 원래 도구의 파일에 남습니다. 여러분 에이전트의 데이터니까요.
deja의 색인, 요약, share, sync 내보내기에는 들어가지 않습니다.

**에이전트가 느려지나요?** 회상 한 번은 로컬 색인에 대한 어휘 질의입니다. 중앙값 0.7–0.8 ms이고
모델을 기다리는 것은 없습니다. 훅은 프로세스 시작과 저장소 신선도 확인을 더하는데, 수 기가바이트 저장소에서
수십 밀리초입니다.

**일하는 방식을 바꿔야 하나요?** 아니요. 회상은 에이전트가 스스로 호출합니다. 자동 회상을 켜면 세션이
열리는 순간 이 프로젝트에서 전에 무엇을 결정했는지 이미 알고 있습니다.

**다른 기억 도구와 무엇이 다른가요?**

| | deja | 기억 플랫폼<br>(Mem0, Letta, memU) | 세션 검색<br>(cass) |
| --- | :-: | :-: | :-: |
| 설치 이전의 작업을 안다 | 예 | 아니요 | 예 |
| 수집 단계가 필요하다 | 아니요, 기록 자체가 기억 | 에이전트나 여러분 코드가 사실을 쓴다 | 아니요 |
| LLM 또는 임베딩 키가 필요하다 | 아니요 | 예 | 선택 |
| 묻지 않아도 회상한다 | 세션 시작 시, 그리고 도구 실행 직전 | 아니요 | 아니요 |

[전체 비교](https://vshulcz.github.io/deja-vu/guide/compare.html)에서 열한 개를 다룹니다.

**Claude Code 세션 기록은 어디 있고 검색할 수 있나요?** `~/.claude/projects` 아래, 세션마다 JSONL 파일
하나입니다. Codex는 `~/.codex/sessions`, Cursor는 SQLite의 `state.vscdb`입니다. `deja search`는 그것들을
제자리에서 읽고, `deja last`는 에이전트별 최근 세션을 보여 주며, `deja view`는 전체 기록을 로컬 페이지
하나로 엽니다. 도구별 경로는
[세션이 저장되는 곳](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)에 있습니다.

**Claude Code 세션 기록이 사라졌는데 잃어버린 건가요?** Claude Code는 30일이 지난 기록을 지웁니다
(`~/.claude/settings.json`의 `cleanupPeriodDays`). `claude --resume`은 남은 것만 보여 줍니다. 정리 전에
deja가 색인한 세션은 파일이 사라진 뒤에도 검색됩니다. 자세한 내용은
[디스크의 세션 파일](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html)에 있습니다.

**전부 지우려면?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## 가이드

기능이 아니라 상황에 따라 썼습니다:

- [코딩 에이전트는 이전 대화를 기억할까?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — 각 에이전트가 세션 사이에 무엇을 남기고 무엇을 잃는지
- [디스크의 세션 파일](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — `~/.claude/projects`가 얼마나 커지고 지우면 무엇을 잃는지
- [에이전트가 방금 컨텍스트를 잃었다](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — 크래시 후, 초기화 후, 또는 세션이 비어서 돌아왔을 때
- [컨텍스트 창이 가득 찼다](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — 압축이 실제로 무엇을 지키는지, 측정과 함께, 그리고 대신 무엇을 할 수 있는지
- [어제 세션을 이어서](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — 모든 에이전트에서 찾아내 원래 도구에서 열기
- [에이전트가 이미 고친 실수를 또 했다](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [그걸 해결한 세션 찾기](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [에이전트가 세션 사이에 잊는 이유](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [각 에이전트가 기록을 두는 곳](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [압축이 잃는 것](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [에이전트 바꾸기](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [에이전트가 한 일 감사하기](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [대화 내보내기](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [기계 사이에서](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [기억에 드는 토큰 비용](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

도구별: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## 자기 기록에서 한번 해 보기

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

설치 10초, 색인 10초 남짓. 다음에 에이전트가 세션을 열면, 이 프로젝트에서 여러분이 무엇을 해결했는지
이미 알고 있습니다. deja를 설치하기 전의 것까지 포함해서.

## 개발

`make build test lint` 다음 [CONTRIBUTING.md](CONTRIBUTING.md)를 보세요.
새 도구는 [파서 레지스트리](docs/ARCHITECTURE.md#source-parsers)에서 시작합니다.
우선순위와 하지 않을 일은 [ROADMAP.md](ROADMAP.md)에 있습니다.

## 라이선스

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
