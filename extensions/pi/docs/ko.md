# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[pi](https://github.com/badlogic/pi-mono)에 같은 머신의 다른 48개 코딩 에이전트(Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes 등) 세션 기록을 로컬 공유 메모리로 연결해 주는 확장입니다. 단일 Go 바이너리 `deja`가 로컬에서 직접 BM25 인덱스를 만들므로 별도 LLM, 임베딩 모델, API 키가 필요하지 않으며, 사용자가 직접 실행하는 업데이트·동기화 명령과 `DEJA_OFFLINE=1`로 끌 수 있는 일일 버전 확인 외에는 네트워크를 사용하지 않습니다.

## 설치

```sh
pi install npm:@vshulcz/pi-deja
```

시스템에 `deja`가 미리 설치되어 있지 않아도 [바이너리 탐색 순서](#바이너리-탐색-순서)에 따라 번들된 바이너리를 사용합니다. 먼저 시스템 바이너리를 설치하려면:

```sh
brew install deja-vu
```

`deja install pi-auto` 또는 `deja install --auto`로 이미 설치된 확장이 감지되면 이 패키지는 자동으로 비활성화되어 중복 주입을 피합니다. `pi.extensions`와 `before_agent_start`를 쓰는 구조이므로 `omp`에서도 동일하게 작동합니다.

## 주요 동작

- **세션 시작(푸터 상태 표시줄)**: 현재 프로젝트의 인덱싱된 세션 수와 최근 히트 정보를 하단 상태바에 표시합니다.
- **매 프롬프트(`before_agent_start`)**: 입력한 프롬프트와 관련된 과거 프로젝트 결정과 디버깅 맥락을 자동으로 찾아 첨부합니다.
- **명령 실패 후(`bash` / `powershell` `tool_result`)**: 명령이 실패하면 과거 같은 오류 뒤에 실제로 문제를 해결했던 명령을 함께 보여줍니다.
- **파일 읽기·편집 시(`read`/`edit`/`write` `tool_result`)**: 수정하려는 파일과 연관된 이전 세션의 판단 근거를 덧붙입니다.
- **컨텍스트 압축 후(`session_compact`)**: 요약 과정에서 빠지기 쉬운 작업 목표, 합의 사항, 미해결 명령을 다시 전달합니다.
- **세션 종료(`session_shutdown`)**: 종료된 세션이 다음 작업에서 검색되도록 인덱스를 갱신합니다.
- **`/deja <query>`**: 대화창에서 모든 에이전트의 기록을 바로 검색합니다.

## 바이너리 탐색 순서

확장은 다음 순서로 `deja` 실행 파일을 찾습니다.

1. `DEJA_BIN` 환경 변수에 지정된 경로
2. `PATH`에 있는 `deja`
3. 일반적인 설치 경로(`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` 등)
4. `@vshulcz/deja-vu` npm 패키지에 포함된 바이너리

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 49개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 개인정보 보호

인덱스는 `~/.cache/deja/index.db`(`DEJA_INDEX_DIR`로 변경 가능)에 저장되며 인덱싱 시 `sk-*`, `ghp_*`, `AKIA*`, `Bearer` 등의 자격 증명을 자동으로 마스킹합니다. 사용자가 직접 `deja embed`로 외부 임베딩을 설정하지 않는 한 검색과 인덱싱은 로컬에서만 실행됩니다. 49개 전체 에이전트 연동(`recall`, `fix`, `blame`, `handoff`)은 `deja install --auto`로 설정할 수 있습니다.

## 라이선스

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
