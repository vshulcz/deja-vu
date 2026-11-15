# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5** · 19,195개 세션 **13.6초** 인덱싱 · agentmemory 대비 **토큰 절반**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 스타](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu)는 Claude Code, Codex, Cursor, opencode 외 44개 코딩 에이전트가 디스크에 이미 기록해 둔 세션 파일을 색인해 답변합니다. 이 확장은 해당 인덱스를 Zed의 에이전트 패널에 context server로 연결해 deja 설치 이전에 작업한 내용까지 검색할 수 있게 해 줍니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 에이전트가 사용하는 도구

하나의 도구 `deja`를 `mode` 파라미터로 호출합니다:

| Mode | 응답 내용 |
|---|---|
| `recall` | 에러 문자열, 함수명, 파일 경로, 플래그와 일치하는 세션. |
| `context` | 의사결정 맥락이 중요할 때 단일 과거 세션의 전체 요약. |
| `blame` | 파일을 수정하거나 삭제하기 전에 해당 파일을 다룬 세션 목록. |
| `fix` | 이 머신에서 같은 에러 직후 실행해 해결한 명령. |
| `how` | 실제 실행 이력에 기반한 빌드·테스트·배포 호출 방법. |
| `orient` | 과거 세션들이 이 프로젝트에서 실행한 명령과 다룬 파일. |
| `remember` | 나중에 다시 꺼내 쓸 수 있는 핵심 결정 사항 저장. |
| `handoff` | 다른 세션의 진행 상태를 이어받아 중단된 지점부터 계속 작업. |

## 설치

Zed 확장 목록에서 설치하세요. 첫 실행 시 Zed가 `deja --version`을 호출해 성공하면 Zed `PATH`의 `deja`를 사용합니다. 그렇지 않으면 릴리스 빌드의 `deja` 바이너리를 확장 디렉터리로 내려받습니다. 확장은 샌드박스 환경에서 실행되므로 일반 설치 경로를 직접 탐색할 수 없어, Zed 실행 `PATH`에 `deja`가 없을 때는 경로를 지정해야 합니다:

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` 역시 Zed를 지원해 `settings.json`에 서버를 바로 기록하므로 CLI가 있다면 더 간편합니다. 두 방식 모두 동일한 ID(`deja-context-server`)를 사용하고 Zed는 ID로 서버를 구분하므로, 어떤 순서로 설치하든 서버 항목은 하나만 유지됩니다. CLI가 작성한 설정을 제거하려면 `deja uninstall zed`를 실행하세요.

색인과 검색은 모두 로컬에서 동작합니다. 네트워크 호출은 없으며 인덱싱 중 자격 증명은 마스킹됩니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1; MemPalace 공개치 96.6%, agentmemory 95.2%) | **토큰 58% 절감** (반복 저장소 작업에서 agentmemory 대비 **절반의 토큰**) | 19,195개 세션 **13.6초** 인덱싱 / 48개 코딩 에이전트 통합 **0.7 ms** 조회 (비교군 7종 중 2위 72초) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

MIT 라이선스 (deja와 동일).

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
