# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

같은 머신에 기록된 다른 52개 코딩 에이전트(Claude Code, Codex, Cursor, Gemini, Zed 등)의 세션 기록을 [OpenClaw](https://openclaw.ai)에 연결합니다. 단일 Go 바이너리 `deja`와 로컬 BM25 색인으로 동작하며 LLM 없이 실행됩니다(`DEJA_OFFLINE=1`로 일일 확인 비활성화 가능).

## 설치

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

바이너리를 시스템에 함께 설치해 두는 것을 권장합니다:

```sh
brew install deja-vu
```

`deja install openclaw-auto` 또는 `deja install --auto`로 이미 구성된 환경에서는 중복 등록을 피하기 위해 자동으로 양보합니다.

## 동작 요약

- **`agent:bootstrap` (`openclaw agent --local` 지원)**: 시작 시 프로젝트의 기존 결정 사항을 컨텍스트로 불러옵니다.
- **`before_prompt_build` (OpenClaw `2026.8.1` 및 `hooks.allowConversationAccess: true` 연동)**: 매 프롬프트마다 관련 기록을 찾아 삽입합니다.
- **`before_compaction`**: 컨텍스트 압축 전 현재 작업, 결론, 명령 실행 이력을 보존합니다.
- **`session_end`**: 종료된 세션을 인덱스에 반영합니다.
- **도구 결과 미들웨어**: 명령 오류나 파일 변경 시 과거 해결 방법과 관련 결정을 함께 표시합니다.
- **`deja_recall` / `deja_fix` / `deja_blame` 도구**: 에이전트가 직접 과거 검색, 오류 수정 명령 조회, 파일 출처 추적을 호출합니다.
- **`/deja <query>`**: 대화창에서 모든 에이전트 기록을 바로 검색합니다.

### DM 출처 영수증(Receipt)

조건에 맞는 DM 응답 하단에는 `reply_payload_sending`을 통해 기울임꼴 출처 안내가 추가됩니다:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

다음 네 조건을 모두 충족할 때만 표시됩니다:

1. 그룹이 아닌 개인 대화(`isGroup: false` / `direct`).
2. 소유자 허용 목록(`commands.ownerAllowFrom` / `allowFrom`) 통과.
3. 블록 스트리밍 비활성화(`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`).
4. `direct` 채널로 나가는 최종 텍스트 응답.

끄려면 설정에서 `"receipt": false`를 지정하세요.

## 설정

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

CLI로 권한 설정하기:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## 바이너리 탐색 순서

`bin` 옵션 -> `DEJA_BIN` -> `PATH` -> 표준 설치 경로 -> `@vshulcz/deja-vu` 패키지 번들.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 53개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 개인정보 보호

인덱스는 `~/.cache/deja/index.db`(`DEJA_INDEX_DIR`로 변경 가능)에 저장되며 `sk-*`, `ghp_*`, `AKIA*`, `Bearer` 자격 증명은 색인 단계에서 제거됩니다. `deja embed`를 별도 지정하지 않는 한 로컬에서만 처리되며, `deja install --auto`로 53개 에이전트 전체에 적용할 수 있습니다.

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
