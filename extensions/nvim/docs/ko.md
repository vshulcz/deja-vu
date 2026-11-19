# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | 한국어 | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 확장 (**< 1 ms** SQLite FTS5 검색 · **~4%** 인덱스 크기 · 수집 시 LLM 토큰 **0**) · [문서](https://vshulcz.github.io/deja-vu/) · [브라우저 데모](https://vshulcz.github.io/deja-vu/view/) · [벤치마크](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Neovim 안에서 Claude Code, Codex CLI, Cursor, opencode, Gemini CLI 및 44개 추가 하네스의 과거 코딩 에이전트 기록을 검색하고, 줄 단위 AI 세션 Blame을 실행하며, 오류 해결 내역을 찾고 에이전트 간 핸드오프 요약을 생성하세요.

![deja-vu 데모](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 사전 요구 사항

[`deja`](https://github.com/vshulcz/deja-vu) 바이너리를 먼저 설치하세요(`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## 설치 (`lazy.nvim`)

`lua/deja/`와 `plugin/deja.lua`가 저장소 루트에 있으므로, `lazy.nvim`이 별도 래퍼 없이 `vshulcz/deja-vu`를 바로 불러옵니다:

```lua
{
  "vshulcz/deja-vu",
  opts = {
    picker = "auto",        -- "auto" | "snacks" | "telescope" | "fzf" | "select"
    limit = 40,
    project_scoped = false,
    keymaps = true,         -- <leader>dr, <leader>dl, <leader>db, <leader>dh, <leader>df
  },
}
```

[`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim)(`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua), 기본 `vim.ui.select`와 바로 동작합니다.

## 명령어 및 키맵

| 명령어 | 기본 키맵 (`keymaps = true`) | 실행 동작 |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | 색인된 모든 세션 전체 텍스트 검색 (`deja search --json`); 비주얼 선택 지원 |
| `:DejaLast [count]` | `<leader>dl` | 설치된 모든 에이전트의 최근 세션 목록 (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | 커서 줄을 작성·수정한 AI 세션 확인 (`deja blame file:line --json`; `!`는 파일 전체) |
| `:DejaHandoff [id]` | `<leader>dh` | 에이전트 간 핸드오프 요약 복사 및 보기 (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | 현재 LSP 진단 또는 에러 문자열 해결 내역 검색 (`deja fix`) |
| `:DejaShow <id>` | — | 마스킹된 세션 기록을 플로팅 Markdown 창으로 열기 (`y` ID 복사, `H` 핸드오프, `R` 분할 창 재개) |

## 상태 표시줄 (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu`에는 `lua/codecompanion/_extensions/deja/init.lua`도 포함되어 있어 `/deja` 슬래시 명령어(채팅 버퍼에 핸드오프 요약을 첨부하는 세션 선택기)와 `#{deja}` / `#{deja:query}` 편집기 컨텍스트를 등록합니다:

```lua
{
  "olimorris/codecompanion.nvim",
  dependencies = { "vshulcz/deja-vu" },
  opts = {
    extensions = {
      deja = { enabled = true },
    },
  },
}
```

Neovim에서 `:checkhealth deja`를 실행하면 바이너리 탐색과 활성 피커 백엔드를 점검할 수 있습니다.

## 벤치마크

| 재현율 (Recall) | 토큰 사용량 | 인덱싱 & 조회 속도 |
|---|---|---|
| LongMemEval-S **97.2% R@5** (470문항 88.9% hit@1) | **토큰 58% 절감** (반복 저장소 작업) | 19,195개 세션 **13.6초** 인덱싱 / 49개 코딩 에이전트 통합 **0.7 ms** 조회 |

## 저장소 메모리 및 에이전트 간 핸드오프

git을 통해 `.deja/memory.jsonl`에 검증된 프로젝트 결정 사항과 시행착오를 공유하세요(`deja repo candidates`, `deja repo add <id>`). 항목은 저장 시 시크릿 마스킹을 거치며, 콘텐츠 해시 정렬과 `merge=union` 설정으로 병렬 브랜치에서도 깔끔하게 병합되어 저장소를 클론한 모든 팀원에게 로드됩니다(신뢰 정책의 `{"activations":{"auto":{"repo":false}}}`로 훅 자동 주입 비활성화 가능). [자세히 보기](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

작업 도중 도구를 바꿔야 하나요? `deja handoff --to <agent>`(또는 MCP로 `handoff` 호출)를 실행하면 현재 세션의 목표, 결정 사항, 수정한 파일, 통과한 검사, 남은 작업을 다른 코딩 에이전트로 넘길 수 있습니다.

[deja-vu](https://github.com/vshulcz/deja-vu)의 일부입니다. MIT.
