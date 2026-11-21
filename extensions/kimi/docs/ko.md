# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu)는 Claude Code, Codex, Cursor, opencode 외 48개 코딩 에이전트가 디스크에 남겨 둔 세션 파일을 색인해 답변합니다. 이 플러그인은 그 인덱스를 Kimi Code에 연결해 프롬프트마다 관련 기억을 가져오고 에이전트가 직접 과거 기록을 검색할 수 있게 합니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 설치

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

이 형식은 최신 릴리스를 고정하고 출처를 기록해 Kimi의 업데이트 확인이 읽을 수 있게 합니다. 전체 저장소 대신 약 10 KB 크기의 `kimi-deja.zip` 릴리스 아카이브도 제공됩니다.

`deja` 바이너리가 없다면 먼저 설치하세요(이 머신의 다른 에이전트도 함께 연결할 수 있습니다):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

플러그인은 관리 복사본에서 로드되므로 설치 후 `/reload` 또는 `/new`를 실행하세요.

## 업데이트

Kimi는 자체 마켓플레이스에서 설치한 플러그인에만 업데이트 알림을 보냅니다. 저장소 설치는 `/plugins install`을 다시 실행해 최신 릴리스를 받으며, 설치된 사본이 현재 `deja`에 포함된 버전보다 뒤처져 있으면 `deja doctor`가 알려 줍니다:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## 제공 구성 요소

- **`deja install kimi-auto`가 등록하는 훅**: 세션의 첫 프롬프트에서 이 프로젝트의 확정 사항을 전달하고(`deja hook-context --once`), 매 프롬프트마다 질문에 맞는 기억을 조회해 Kimi가 해당 턴에 붙입니다(`deja hook-prompt`, 일치 항목이 없으면 생략). 컴팩션 직전 접힐 턴을 읽어 다음 프롬프트가 진행 중이던 맥락을 이어가며(`deja hook-precompact`), `SessionEnd`에서 활성 스탬프를 제거합니다(`deja hook-session-end`).
- **도구**: 플러그인이 `deja mcp`를 MCP 서버로 등록해 `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember`, `handoff` 모드를 가진 단일 `deja` 도구를 노출합니다.
- **`/deja:recall <query>`**: 사용자가 직접 과거 기록을 조회할 때 씁니다.
- **`deja-history` 스킬**: 세션 시작 시 로드되어 에이전트가 다시 디버깅하기 전에 먼저 과거 기록을 확인하도록 안내합니다.

## 사용되는 바이너리 우선순위

`DEJA_BIN` → 직접 설치한 `deja`(`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`) → `PATH`의 `deja` 순서로 찾습니다. 직접 실행한 `deja update` 또는 `brew upgrade deja-vu`가 항상 우선합니다.

## `deja install kimi`도 함께 실행한 경우

CLI는 `~/.kimi-code/mcp.json`에 같은 MCP 서버를, `config.toml`에 같은 훅을 씁니다. Kimi는 플러그인 서버를 별도 네임스페이스(`plugin-deja:deja`) 아래에서 띄우므로 둘 다 실행되면 모든 도구가 두 번씩 나열되고 리콜도 두 번 붙습니다.

이 플러그인은 훅 실행 시점과 서버 시작 시점에 설치기 구성을 확인해 발견되면 스스로 물러납니다(`deja install`이 관리하는 사본이 항상 최신이기 때문입니다). `deja uninstall kimi`를 실행하면 재설치 없이도 플러그인이 즉시 제어권을 되찾습니다.

플러그인만 사용 중일 때도 `deja doctor`는 kimi 상태를 `plugin`으로 표시해 미설정으로 오인하지 않습니다.

감지할 수 없는 두 가지 예외가 있습니다: `deja` 외의 이름으로 수동 추가한 MCP 항목(함께 실행됨)과 설치기 마커 주석 없이 직접 작성해 `deja`를 호출하는 훅입니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 52개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

MIT

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
