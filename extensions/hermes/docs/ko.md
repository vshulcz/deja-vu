# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5** · 19,195개 세션 **13.6초** 인덱싱 · agentmemory 대비 **토큰 절반**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 스타](https://github.com/vshulcz/deja-vu)**

이 머신에 이미 존재하는 코딩 세션(Claude Code, Codex, Cursor, Hermes 자체 및 deja가 읽는 다른 모든 에이전트, 아무것도 설치하기 전 수개월간의 기록 포함)을 바탕으로 답변하는 Hermes 메모리 프로바이더입니다. 별도 모델, API 키, 백그라운드 서버 없이 단일 Go 바이너리 [deja](https://github.com/vshulcz/deja-vu)가 로컬에서 기록을 색인합니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 설치

먼저 `PATH`에 `deja`가 있어야 합니다:

```bash
brew install deja-vu        # 또는: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # Hermes와 이 머신의 모든 지원 에이전트 연결
hermes plugins install deja-memory   # 또는 GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

또는 `hermes memory setup`에서 `deja-memory`를 선택하면 바이너리를 확인한 뒤 프로바이더를 활성화합니다.

## 동작 방식

- **세션 첫 턴 이전**: deja 훅이 Claude Code와 Codex에 전달하는 프로젝트 요약(최근 작업과 확정 사항)을 Hermes에 전달합니다. 그 후로는 방금 입력한 질문이 과거 기록과 맞을 때만 붙이며, 대부분 턴에는 아무것도 추가하지 않습니다.
- **불러온 출처 표시**: deja가 Claude Code에서 보여주는 안내(기억이 나온 에이전트 영수증, 세션 시작 회상, 빌드 및 주간 노트)는 대화형 CLI와 `hermes chat -q`에서 Hermes의 리콜 줄 `🧠 deja · …`에 실립니다. 모델 출력 본문은 그대로 유지됩니다. Hermes 0.17에는 리콜 줄이 없고 `hermes -z`·`hermes chat -Q`도 출력하지 않으며, TUI는 상태 표시줄에 잠시 띄우기만 하고 게이트웨이 채팅은 다른 사람이 보는 그룹일 수 있어 이때는 표시 가능한 세션으로 노트를 보관합니다.
- **cron 작업은 자동 회상 제외**: Hermes 플랫폼이 `cron`이면 프로바이더가 자동으로 아무것도 추가하지 않으며, 작업이 직접 deja 도구를 호출할 때는 정상 응답합니다.
- **세 가지 도구**: `deja_recall`(과거 세션 검색), `deja_fix`(지난번 같은 에러 후 실행한 명령), `deja_blame`(파일을 편집한 세션 목록).
- **노트 우선순위 유지**: Hermes가 `MEMORY.md`나 `USER.md`에 추가한 항목도 deja 노트로 보관되어 주변 세션보다 상위에 노출됩니다.
- **컴팩션 대응**: 컴팩션 직전 진행 중이던 맥락을 다음 턴으로 넘기고, 세션 종료 시 활성 표시를 해제합니다.

메모리 프로바이더 혼자서는 할 수 없는 한 가지: 방금 실패한 명령 옆에 과거 수정 내역을 바로 붙이는 일입니다. 이는 플러그인 훅 `transform_tool_result`를 타는데 Hermes 0.17.0은 메모리 프로바이더 컨텍스트에서 `register_hook`을 무효화합니다(`plugins/memory/__init__.py`). `deja install hermes-auto`가 해당 훅 플러그인까지 함께 설치합니다.

Hermes 턴을 별도로 복제해 쓰지 않으며, Hermes가 저장한 데이터를 다음 갱신 시점에 그대로 읽습니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1; MemPalace 공개치 96.6%, agentmemory 95.2%) | **토큰 58% 절감** (반복 저장소 작업에서 agentmemory 대비 **절반의 토큰**) | 19,195개 세션 **13.6초** 인덱싱 / 48개 코딩 에이전트 통합 **0.7 ms** 조회 (비교군 7종 중 2위 72초) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## `deja install`과 동일한 코드

`deja install hermes-auto`는 이 프로바이더를 `~/.hermes/plugins/deja-memory`에 기록하면서 MCP 서버와 `pre_llm_call` 훅 플러그인도 같이 씁니다. 여기 있는 파일은 동일한 소스(`cmd/deja/install_hermes_memory.go`)에서 생성되며, 유일한 차이는 경로를 고정하지 않고 PATH에서 `deja`를 찾는다는 점입니다. 둘이 어긋나면 `TestHermesExtensionIsTheProviderInstallWrites`가 실패합니다.

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
