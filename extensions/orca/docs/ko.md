# deja-vu for Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

`deja`용 Orca v2 `trusted-worker` 플러그인입니다. `deja`를 설치하기 전의 세션을 포함해 49개 AI 코딩 도구(Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca 등)의 세션 메모리를 Orca에서 바로 불러옵니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 설치

사전 요구 사항: `PATH`(또는 `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin`, `%LOCALAPPDATA%\Programs\deja`)에 [`deja`](https://github.com/vshulcz/deja-vu#install)가 설치되어 있어야 합니다.

Orca(`>=1.4.248`)에 `extensions/orca` 플러그인 디렉터리를 설치하세요.

Orca 내부에서 네이티브 MCP, 스킬 파일, `SessionStart`, `PreCompact`, `SessionEnd` 및 실패한 명령 수정 제안을 모두 사용하려면 다음 명령도 함께 실행하세요:

```sh
deja install orca-auto
```

(`deja install orca`는 수명 주기 훅 없이 MCP, 스킬, `/deja`만 설치하며 `deja install orca-auto` 또는 `deja install --auto`가 전체 훅을 구성합니다.)

`deja install orca-auto`가 이미 Orca `settings.json`에 네이티브 `UserPromptSubmit` 훅을 기록해 둔 경우, 이 워커는 자동으로 물러나 `<deja-recall>` 프레임이 중복 주입되지 않도록 합니다.

## 플러그인 제공 기능

- **Hooks**
  - `agent.prompt.before`: 활성 작업 공간 경로를 확인하고 Orca의 2초 훅 예산(`1.3s` 타임아웃, fail-open) 안에서 관련 `<deja-recall>` 컨텍스트를 프롬프트에 주입합니다.
  - `agent.tool.before`: `Agent` 또는 `Task` 하위 에이전트를 생성할 때 `input.prompt`에만 `<deja-recall>` 컨텍스트를 추가합니다(`Bash`, `Read`, `Edit` 등 다른 도구 입력은 수정하지 않습니다).
  - `agent.tool.after`: Orca 호스트 API에서는 관찰 전용(`next`가 무시되며 대기하지 않음)이므로 실패 명령 `fix` 전달은 `deja install orca-auto`가 담당합니다.
- **Commands**
  - `deja.orient` (`deja: Orient on workspace`): 현재 작업 공간에서 `deja hook-context --plain`을 실행하고 요약 알림을 띄웁니다.
  - `deja.recall` (`deja: Recall past sessions`): `deja search --limit 5 -- <query>`(질의가 없으면 `deja last -n 5`)를 실행해 결과를 표시합니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1; MemPalace 공개치 96.6%, agentmemory 95.2%) | **토큰 58% 절감** (반복 저장소 작업에서 agentmemory 대비 **절반의 토큰**) | 19,195개 세션 **13.6초** 인덱싱 / 49개 코딩 에이전트 통합 **0.7 ms** 조회 (비교군 7종 중 2위 72초) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

[MIT](../LICENSE)

도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
