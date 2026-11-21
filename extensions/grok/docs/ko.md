# Grok Build용 deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Claude Code, Codex, Cursor, opencode, Zed 등 47개 추가 코딩 에이전트 세션 기록에서 가져온 로컬 영구 메모리입니다. 단일 Go 바이너리 `deja`로 로컬에서 동작하며 `DEJA_OFFLINE=1`로 일일 확인도 끌 수 있습니다.

## 설치

플러그인 번들에는 실행 파일이 포함되지 않으므로 먼저 `deja`를 설치하세요:

```sh
brew install deja-vu
# 또는:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

그다음 마켓플레이스에서 플러그인을 설치합니다(공식 마켓플레이스 PR: [xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

또는 `deja install --auto`로 `~/.grok/config.toml`(` [mcp_servers.deja]`)과 `~/.grok/hooks/deja.json`에 직접 구성할 수 있습니다.

## 제공 기능

- **`deja` MCP 도구**(8개 모드: `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **`deja-history` 스킬** 및 **`/deja:recall <query>`** 명령
- **5개 훅(`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse`(`updatedInput`), `PostToolUse`(Grok 1.0.41 `Bash` 실행 지원), `PreCompact`, `SessionEnd`

## 두 경로를 함께 써도 안전합니다

`~/.grok/config.toml`에 `[mcp_servers.deja]`가 있거나 `~/.grok/hooks/deja.json`에 deja 훅이 등록되어 있으면 플러그인이 자동으로 양보하므로 중복 동작하지 않습니다. 제거하려면 `deja uninstall grok`을 실행하세요.

## 바이너리 탐색 순서

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## 미설치 상태에서의 안전한 대기

`${GROK_PLUGIN_ROOT}`와 `.mcp.json`을 통해 연결됩니다. Grok Build 1.0.5 환경에서 `deja`가 없으면 `grok mcp doctor` 및 `grok inspect`에 `handshake failed: connection closed`가 표시되며, `deja` 설치 즉시 정상 연결됩니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 52개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## ## 라이선스

MIT
