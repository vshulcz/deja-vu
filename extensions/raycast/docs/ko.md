# deja-vu for Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Raycast에서 Claude Code, Codex, Cursor, OpenCode, Gemini CLI 외 48개 코딩 에이전트 세션을 검색하고 바로 재개하세요.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 사전 요구 사항

[`deja-vu`](https://github.com/vshulcz/deja-vu)를 먼저 설치합니다:

```sh
brew install deja-vu && deja install --auto
```

## 명령어

- **Search Sessions**: Mac에 저장된 모든 코딩 에이전트 세션을 전문 검색하며(`deja search --json --limit 25 -- <query>`), 정확 일치 항목과 가장 가까운 관련 세션 결과를 구분해 보여줍니다.
- **Recent Sessions**: 설치된 모든 에이전트에서 최근 100개 세션을 탐색하고(`deja last 100 --json`) 메타데이터(`Agent`, `Project`, `Started`, `Updated`, `Session ID`)를 미리 봅니다.

## 동작 (Actions)

- **Show Session**: 시크릿이 제거된 대화 내역을 Raycast 안에서 바로 읽습니다(`deja show <id> --json --limit 200`).
- **Resume in Terminal**: `Terminal.app`에서 해당 에이전트의 네이티브 재개 명령(`deja resume <id>`)을 실행합니다.
- **Copy Resume Command**, **Copy Session ID**, **Open Project Folder**.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 53개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 로컬 개발

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
