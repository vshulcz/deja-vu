# `@vshulcz/magpie-deja` — deja-vu용 Magpie 게이트웨이 훅

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

**[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> [Magpie](https://github.com/yetone/magpie)를 거치는 모든 요청에 같은 머신의 다른 51개 코딩 에이전트(Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes 등) 세션 기록을 자동으로 보강합니다.

Magpie의 OpenAI(`chat`, `responses`), Anthropic(`anthropic`), Gemini(`gemini`) 프로토콜 전체에서 단일 Go 바이너리 `deja`가 로컬 BM25 색인을 조회합니다. 별도의 LLM이나 클라우드 임베딩 없이 동작하며, `DEJA_OFFLINE=1`로 일일 버전 확인도 끌 수 있습니다.

## 설치

```sh
magpie plugin add @vshulcz/magpie-deja
```

`@vshulcz/deja-vu`, `brew install deja-vu`, 또는 공식 `install.sh` 스크립트로 `deja`를 설치한 뒤 `deja index`로 색인을 생성해야 합니다. 52개 코딩 에이전트 전체에 `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff` 도구를 연결하려면:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

요청에 이미 `<deja-memory>` 또는 `<deja-recall>` 블록이 있으면 중복 주입을 건너뜁니다.

## 동작 방식 및 데이터 경계

1. **`onRequest`에서 `deja hook-prompt --plain` 실행**: `~/.cache/deja/index.db`(`DEJA_INDEX_DIR`로 변경 가능)를 기본 `800ms` 제한 안에 조회하여 `<deja-memory>`를 삽입합니다. **외부 전송 주의**: 주입된 컨텍스트는 게이트웨이를 통해 업스트림 LLM 제공자로 전송됩니다.
2. **작업 디렉터리 격리**: `ctx.cwd` 기준으로만 조회하며(`allowUnscoped: false`), `process.cwd()`로 폴백해 `deja hook-prompt`에 잘못 전달하지 않습니다.
3. **로컬 출처 제한**: `recallRemote` 기본값은 `false`이며 `ctx.origin === "local"` 또는 `ctx.origin === "magpie-key"`일 때만 동작합니다.
4. **이중 시크릿 마스킹**: 색인 시점에 한 번, 주입 직전 `deja.hooks.mjs`와 동일한 `maskSecrets`로 `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, URL 자격 증명, `api_key=...`를 한 번 더 제거합니다.
5. **반복 방지 및 캐시 보존**: `<deja-memory`, `<deja-recall>`, `[deja —` 패턴을 감지하면 재삽입을 피하고 프롬프트 접두사 캐시를 유지합니다.

## 옵션

| 옵션 | 기본값 | 설명 |
|---|---|---|
| `requestTimeoutMs` | `800` | `deja` 호출 제한 시간(`50`–`5000` ms) |
| `maxBytes` | `4000` | 주입 최대 바이트 수(`256`–`32768`) |
| `recallRemote` | `false` | 원격 출처 요청에 대한 회상 허용 여부 |
| `allowUnscoped` | `false` | `ctx.cwd`가 없을 때 전역 조회 허용 여부 |
| `cwd` | `""` | 프로젝트 경로 수동 지정 |
| `skipAgents` | `[]` | 주입을 건너뛸 에이전트 목록(예: `["claude-code"]`) |
| `bin` | `""` | `deja` 바이너리 경로 |

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 52개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
