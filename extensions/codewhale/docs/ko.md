# deja-vu for CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

CodeWhale용 Claude 호환 플러그인 번들입니다. 48개 AI 코딩 도구의 로컬 세션을 대상으로 `deja` MCP 서버(`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), `deja-history` 스킬, `/deja` 슬래시 명령을 제공합니다.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 설치

사전 요구 사항: `PATH`에 [`deja`](https://github.com/vshulcz/deja-vu#install)가 설치되어 있어야 합니다.

CodeWhale에서 `/plugin install`을 사용해 저장소 하위 디렉터리(`extensions/codewhale`)로부터 이 번들을 설치하세요.

CodeWhale의 Claude 플러그인 호환 로더는 MCP 서버, 스킬, 슬래시 명령만 가져오고 `hooks`와 `lspServers`는 무시하므로, `~/.codewhale/config.toml`에 네이티브 CodeWhale 수명 주기 훅(`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`)까지 연결하려면 `deja install codewhale-auto`(또는 `deja install --auto`)를 실행하세요(`deja install codewhale`은 MCP와 `tools.always_load`만 연결합니다):

```sh
deja install codewhale-auto
```

## 번들 구성

- `.claude-plugin/plugin.json`: 플러그인 메타데이터 (`name: deja-vu`)
- `.mcp.json`: `deja mcp` stdio 서버 항목
- `skills/deja-history/SKILL.md`: `deja` 모드별 스킬 지침
- `commands/deja.md`: `/deja` 슬래시 명령

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1; MemPalace 공개치 96.6%, agentmemory 95.2%) | **토큰 58% 절감** (반복 저장소 작업에서 agentmemory 대비 **절반의 토큰**) | 19,195개 세션 **13.6초** 인덱싱 / 48개 코딩 에이전트 통합 **0.7 ms** 조회 (비교군 7종 중 2위 72초) |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

## 라이선스

[MIT](../LICENSE)

도움이 되었나요? [GitHub에서 deja-vu에 스타 남기기](https://github.com/vshulcz/deja-vu).
