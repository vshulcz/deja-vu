# deja-vu for VS Code, Cursor, Windsurf & Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**이미 고친 문제를 코딩 에이전트가 처음부터 다시 디버깅하는 일을 멈춥니다.**

[deja-vu](https://github.com/vshulcz/deja-vu)는 Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code 외 41개 코딩 에이전트가 디스크에 이미 기록해 둔 세션 트랜스크립트를 단일 Go 바이너리로 로컬 인덱싱합니다. LLM 호출이나 백그라운드 데몬이 없습니다.

**[★ GitHub에서 스타 남기기](https://github.com/vshulcz/deja-vu)** · **[문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 확장 기능 소개

IDE 상태 표시줄에 실시간 `deja statusline` 상태(에이전트별 인덱싱된 세션 수, 오늘 제공된 메모리 리콜 수, 작업 공간 이력)를 표시합니다. `deja`가 아직 설치되어 있지 않다면 상태 표시줄 항목을 클릭해 터미널에서 `npx -y @vshulcz/deja-vu install --auto`를 실행하고 머신에서 감지된 모든 에이전트를 한 번에 연결합니다.

## 빠른 설치

```sh
brew install deja-vu && deja install --auto
# Homebrew 없이 설치:
npx -y @vshulcz/deja-vu install --auto
```

CLI에서 상태 표시줄 확장 설치:

```sh
code --install-extension vshulcz.deja-status
```

## 실측 영향

| 벤치마크 | 결과 |
|---|---|
| LongMemEval-S 검색 정확도 (정제된 470문항) | **97.2% R@5**, **88.9% hit@1** (MemPalace 공개치 96.6%, agentmemory 95.2%) |
| 반복 저장소 작업의 토큰 비용 | **58% 절감** — agentmemory 대비 **절반 수준** (`52,815` vs 콜드 상태 `103,443`, 11쌍 비교) |
| 인프로세스 어휘 검색 지연 시간 | 49개 코딩 에이전트 통합 중앙값 **0.7–0.8 ms** |
| 실제 19,195개 세션 초기 인덱싱 시간 | **13.6 s**, API 키 불필요 (7개 도구 중 차순위 72 s) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
