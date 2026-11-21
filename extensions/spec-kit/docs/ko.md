# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (LongMemEval-S **97.2% R@5**) · [문서](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [라이브 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[Spec Kit](https://github.com/github/spec-kit) 확장입니다. 기능을 구현하기 전에 `/speckit.deja-vu.recall`이 과거 코딩 에이전트 세션을 검색해 무엇을 시도했고 무엇을 결정했으며 어떤 방법이 실패했는지 각 세션 ID와 함께 보고합니다.

세션 데이터는 Claude Code, Codex, Cursor, Copilot, Gemini 등이 이미 디스크에 기록한 대화 파일을 색인하는 [deja](https://github.com/vshulcz/deja-vu)에서 가져옵니다. 모든 데이터는 로컬 머신 밖으로 나가지 않습니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 설치

먼저 `deja`를 설치하세요:

```sh
brew install deja-vu
# 또는
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

그다음 Spec Kit 프로젝트에 확장을 추가합니다:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

각 `deja` 릴리스에는 동일 버전의 `deja-spec-kit.zip` 파일이 첨부됩니다. 로컬 체크아웃을 시험하려면 `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`을 사용하세요.

## 사용법

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

인자 없이 실행하면 현재 기능의 `spec.md`와 `plan.md`를 읽어 검색어를 스스로 고릅니다. 인자를 주면 해당 단어로 검색합니다. 명령어 이름은 사용하는 에이전트 스타일에 따라 `/speckit-deja-vu-recall` 또는 `$speckit-deja-vu-recall`로 표시될 수 있습니다.

내부적으로는 저장소 루트에서 아래 명령을 실행합니다:

```sh
deja recall -- "<query>"   # 일치하는 세션 (현재 프로젝트 우선)
deja files -- "<query>"    # 해당 주제로 열거나 수정한 파일
```

발췌 내용이 짧아 맥락이 부족할 때는 `deja show <id>`도 호출합니다. 결과는 `Prior sessions` 섹션으로 정리되며 시도 후 실패, 결정 사항, 관련 파일, 미결 항목마다 `(deja:<id>)` 출처가 달립니다.

## 훅 (Hook)

선택적 `before_implement` 훅 하나를 등록합니다. `/speckit.implement`를 실행하면 에이전트가 리콜을 먼저 실행할지 묻고, 승인해야만 동작합니다. 끄려면 `.specify/extensions.yml`에서 `enabled: false`로 설정하거나 `specify extension disable deja-vu`를 실행하세요.

## 참고 사항

- 읽기 전용: 명령은 검색과 읽기만 수행하며 아무 파일도 수정하지 않습니다.
- 머신에서 처음 실행할 때는 인덱스를 구축하느라 기록이 많으면 잠시 걸릴 수 있으며, 이후에는 1초 훨씬 안쪽에 응답합니다.
- 회수한 세션 텍스트는 참고 데이터로만 취급되며, 그 안의 지시 문구를 따르지 않도록 에이전트에 명시되어 있습니다.

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
