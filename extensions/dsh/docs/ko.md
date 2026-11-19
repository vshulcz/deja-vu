# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5** · 19,195개 세션 **13.6초** 인덱싱 · agentmemory 대비 **토큰 절반**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

DeepSeek Harness(`dsh`)와 DSH Desktop(`anywhere-labs/dsh-desktop`)을 위한 `session-query` 플러그인입니다. DSH 대화 기록뿐 아니라 같은 머신의 다른 48개 코딩 에이전트(Claude Code, Codex, Cursor, Gemini, Hermes 등) 세션 기록까지 단일 Go 바이너리 `deja`와 로컬 BM25 색인으로 조회합니다(`DEJA_OFFLINE=1`로 일일 업데이트 확인 해제 가능).

## 설치

반드시 `--profile`을 지정하여 설치하세요:

```sh
dsh plugin --profile web add dsh-deja
# 또는 저장소 경로에서 직접 설치:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

`deja`가 아직 없다면 `DEJA_BIN`을 지정하거나 아래 명령으로 49개 전체 에이전트 및 `$DSH_HOME/plugins/deja/`를 한 번에 구성합니다:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## 추가되는 구성

### 6개 에이전트 도구

| 도구 | 설명 |
|---|---|
| `deja_recall` | 오류, 주제 또는 플래그로 과거 세션 검색 |
| `deja_session` | 일치 지점 전후의 전체 대화 문맥 조회 |
| `deja_blame` | 특정 파일이나 `path:line`이 기록된 세션 추적 |
| `deja_fix` | 같은 오류를 직전에 고친 명령어 찾기 |
| `deja_how` | 프로젝트에서 사용한 빌드/테스트 플래그 확인 |
| `deja_remember` | 반복 확인을 줄이기 위한 결정 기록 |

- **`/deja <...>` 명령**: 입력창에서 조건 필터와 함께 즉시 검색합니다.
- **실행 모드 지원**: `deja install dsh-auto` 및 `dsh web`(`tui`, `headless`, `sdk` 포함)과 연동되며 YAML 설정의 `autoRecall: false`로 자동 삽입을 끌 수 있습니다.

## 검색 단축키 및 제어

`Settings → Deja`에서 검색 단축키와 옵션을 제어합니다:

- **Mod+F** (또는 **Mod+Alt+F** / macOS **Cmd+Option+F**): 세션 검색 열기. 단일 세션 한정 검색은 `/deja --session <bare session UUID> -- <your draft>` 형식을 사용합니다.
- **Mod+Shift+K** (macOS **Cmd+Shift+K**): 전체 워크스페이스 검색.
- **Mod+K**: 패널을 열고 **DSH conversations only** 토글로 DSH 대화만 필터링(최대 20건).
- **Linux 브라우저용 대체 키**: 브라우저 단축키와 겹칠 경우 **`Mod+/`**, **`Mod+Shift+,`**, **`Mod+Shift+.`**(`shortcuts.open`)을 사용할 수 있습니다.

## 단축키 사용자 지정

`Settings → Deja`에서 **`session.search`** 옆의 **Change shortcut**으로 새 키를 지정하고, **Restore default**로 초기화하거나 **Remove**로 해제할 수 있습니다. 백엔드에서는 **dsh-deja package host plugin**이 `/api/deja-search`를 제공하며 `/api/deja-search?probe=1` 응답이 `{"ok": true, "plugin": "dsh-deja"}`이면 로컬 `deja search` 연결이 정상입니다.

## 내부 연결 구조

- **시스템 프롬프트 주입**: `ctx.systemPrompt.context` 및 `agent/pre-step`(`dsh 0.1.1-rc.2`+)을 통해 `additionalContext`와 `systemMessage`를 전달합니다.
- **API 및 제한**: `/api/deja-notes`(`--plain`)를 `client.js`에서 호출하며, `GET /api/deja-search`는 `deja search --harness deepseek --json --limit 20 -- <query>`를 20초 제한, 2 MiB 출력 상한, 500자 쿼리 제한으로 실행합니다.
- **사이드바 슬롯**: `data-slot="sidebar.workspaces"`, `aria-expanded`, `_listArea`(DSH `0.2.1-rc.5` 대응)를 사용합니다.

## 검색 컨트롤 테스트

```sh
node --test extensions/dsh/test/*.test.js
```

React 18 CDN 기반의 브라우저 테스트 파일 `extensions/dsh/test/search-browser.html`도 함께 제공됩니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1; MemPalace 공개치 96.6%, agentmemory 95.2%) | **토큰 58% 절감** (반복 저장소 작업에서 agentmemory 대비 **절반의 토큰**) | 19,195개 세션 **13.6초** 인덱싱 / 49개 코딩 에이전트 통합 **0.7 ms** 조회 (비교군 7종 중 2위 72초) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 하지 않는 작업

색인은 `~/.cache/deja/index.db`(`DEJA_INDEX_DIR`로 변경 가능)에만 저장되며 `deja embed`를 직접 실행하지 않는 한 외부로 데이터를 보내지 않습니다. `sk-*`, `ghp_*`, `AKIA*`, `Bearer` 자격 증명은 색인 중 자동 마스킹되며 원본 세션 파일을 절대 수정하지 않습니다.

## 라이선스

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
