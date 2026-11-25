# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm, Rider(`2024.2+`)를 위한 로컬 에이전트 간 세션 검색, AI 라인 Blame, 핸드오프 플러그인입니다.

[deja-vu](https://github.com/vshulcz/deja-vu)는 Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf 외 44개 코딩 에이전트가 디스크에 남긴 세션 기록을 로컬 BM25 인덱스에 색인합니다. 출력 전 시크릿을 마스킹하며 클라우드 서비스 없이 동작합니다.

**[문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 기능

- **상태 표시줄 위젯**: IDE 상태 표시줄 오른쪽에 현재 프로젝트의 실시간 `deja statusline` 통계를 표시합니다. 클릭하면 `deja-vu` 액션 팝업이 열립니다.
- **AI 세션 검색 (`Tools → deja-vu → Search AI Sessions...`)**: 지원하는 53개 전체 코딩 에이전트의 로컬 세션을 검색(`deja search --json`)하고 선택한 기록을 읽기 전용 Markdown 편집기 탭에서 엽니다.
- **최근 AI 세션 (`Tools → deja-vu → Recent AI Sessions`)**: 이 머신의 최근 50개 세션(`deja last 50 --json`)을 빠른 검색 필터와 함께 나열합니다.
- **현재 줄 AI Blame (편집기 컨텍스트 메뉴 / `Tools → deja-vu → AI Blame Current Line`)**: `deja blame <file>:<line> --json`(일치 항목이 없으면 `<file>`로 폴백)을 실행하여 커서 위치의 줄을 작성하거나 수정한 AI 세션을 엽니다.
- **에이전트 간 핸드오프 복사 (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: 프로젝트 루트에서 `deja handoff`를 실행하고 요약을 클립보드에 복사한 뒤 읽기 전용 Markdown 탭으로 엽니다.

## 설치

1. `deja` CLI를 설치하고 로컬 코딩 에이전트를 연결합니다:

```sh
brew install deja-vu && deja install --auto
# Homebrew가 없는 경우:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. JetBrains 플러그인([JetBrains Marketplace의 `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory))을 설치합니다:

- **JetBrains Marketplace에서 설치**: [plugins.jetbrains.com에서 직접 설치](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)하거나 **Settings → Plugins → Marketplace**에서 `deja-vu`를 검색하세요.
- **디스크에서 설치**: [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest)에서 `deja-jetbrains.zip`을 내려받아 **Settings → Plugins → ⚙️ → Install Plugin from Disk...** 로 설치하세요.
- **CLI로 설치**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(사용 중인 IDE에 맞게 `idea`를 `goland`, `pycharm`, `webstorm`, `rustrover` 또는 `clion`으로 바꾸세요.)*

## 소스에서 빌드

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

설치 가능한 아카이브는 `build/distributions/`에 생성됩니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 53개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
