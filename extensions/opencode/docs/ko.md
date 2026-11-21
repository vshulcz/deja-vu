# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[opencode](https://opencode.ai)에서 같은 머신의 다른 51개 코딩 에이전트(Claude Code, Codex, Cursor, Gemini, Hermes, Zed 등) 기록을 함께 활용할 수 있게 해 주는 플러그인입니다. 단일 Go 바이너리 `deja`와 로컬 BM25 색인을 사용하며 LLM 없이 작동합니다(`DEJA_OFFLINE=1`로 일일 업데이트 확인 비활성화 가능).

## 설치

`opencode.ai/config.json`에 추가:

```json
{ "plugin": ["opencode-deja"] }
```

(opencode 2.x는 `"plugins": ["opencode-deja"]` 또는 `opencode plugin add opencode-deja` 사용) 혹은 `deja install --auto`로 `opencode.json`과 `plugins/deja.js`를 자동 생성할 수 있습니다.

## 제공 기능

### 6가지 온디맨드 도구

| 도구 | 설명 |
|---|---|
| `deja_recall` | 오류, 심볼 또는 주제로 이전 세션 검색 |
| `deja_session` | 일치한 지점 전후의 세션 내용 읽기 |
| `deja_blame` | 특정 파일이나 `path:line`이 작성된 세션 조회 |
| `deja_fix` | 같은 오류를 지난번에 해결한 명령 확인 |
| `deja_how` | 프로젝트에서 실제로 사용하던 플래그 및 실행 방식 회상 |
| `deja_remember` | 확정된 결론을 향후 작업을 위해 기록 |

### 4가지 자동 기억 기능

- **세션 시작 컨텍스트**: 1.x에서는 토스트 알림, 2.x에서는 시스템 프롬프트로 `deja-vu — from Codex CLI, 4 months ago: "…"` 형식으로 전달.
- **20분 쿨다운 및 서브에이전트 제외**: 동일 프로젝트 내 중복 알림을 막고 서브에이전트에는 불필요한 컨텍스트를 넣지 않음.
- **실패 명령 및 파일 결정 보강**: 실행 오류나 파일 작업 직후 관련 이력을 덧붙임.
- **압축(Compaction) 보호**: 요약 전 현재 작업과 핵심 명령 목록을 보존.

## 옵션

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(2.x는 `"plugins"` 키 사용)

- `autoRecall`: 자동 주입 사용 여부(기본값 `true`)
- `tools`: 6개 도구 노출 여부(기본값 `true`)
- `bin`: `deja` 바이너리 경로 직접 지정

52개 전체 도구에 `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff` 8모드 MCP를 연결하려면 `deja install --auto`를 실행하세요([컨텍스트 손실 없는 에이전트 전환 가이드](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)).

## 바이너리 탐색 순서

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> npm 번들 바이너리.

## 개인정보 보호

인덱스는 `~/.cache/deja/index.db`(`DEJA_INDEX_DIR`로 변경 가능)에 저장되며 `sk-*`, `ghp_*`, `AKIA*`, `Bearer` 토큰을 구축 시점에 제거합니다. `deja embed`를 별도로 지정하지 않으면 외부 통신을 하지 않습니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 52개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## ## 라이선스

MIT
