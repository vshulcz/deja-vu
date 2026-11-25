# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

為 IntelliJ IDEA、GoLand、PyCharm、WebStorm、RustRover、CLion、PhpStorm 與 Rider（`2024.2+`）提供本地端跨代理會話檢索、AI 程式碼行溯源（AI Line Blame）與跨代理接力。

[deja-vu](https://github.com/vshulcz/deja-vu) 會在本地 BM25 索引中索引 Claude Code、Codex、OpenCode、Cursor、Gemini CLI、Junie、GitHub Copilot、Aider、Windsurf 及其他 44 款程式設計代理寫入磁碟的會話紀錄，輸出前自動脫敏，無需任何雲端服務。

**[文件](https://vshulcz.github.io/deja-vu/)** · **[評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 功能特色

- **狀態列小工具**：在 IDE 狀態列右側即時顯示目前專案的 `deja statusline` 統計資訊，點擊即可開啟 `deja-vu` 操作彈窗。
- **搜尋 AI 會話（`Tools → deja-vu → Search AI Sessions...`）**：跨全部 53 款支援的程式設計代理檢索本地會話（`deja search --json`），並在唯讀 Markdown 編輯器分頁中開啟所選會話紀錄。
- **最近 AI 會話（`Tools → deja-vu → Recent AI Sessions`）**：列出本機最近 50 筆會話（`deja last 50 --json`），支援即時快速篩選。
- **AI 目前行溯源（編輯器右鍵選單 / `Tools → deja-vu → AI Blame Current Line`）**：執行 `deja blame <file>:<line> --json`（若無精確行紀錄則退回 `<file>`），開啟撰寫或修改游標所在行的 AI 會話。
- **複製跨代理接力摘要（`Tools → deja-vu → Copy Cross-Agent Handoff`）**：在專案根目錄執行 `deja handoff`，將摘要複製到系統剪貼簿並在唯讀 Markdown 分頁中開啟。

## 安裝

1. 安裝 `deja` CLI 並串接本地程式設計代理：

```sh
brew install deja-vu && deja install --auto
# 或不使用 Homebrew：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. 安裝 JetBrains 外掛（[JetBrains Marketplace 上的 `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)）：

- **從 JetBrains Marketplace 安裝**：[直接在 plugins.jetbrains.com 安裝](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)，或開啟 **Settings → Plugins → Marketplace** 搜尋 `deja-vu`。
- **從本機磁碟安裝**：自 [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) 下載 `deja-jetbrains.zip`，透過 **Settings → Plugins → ⚙️ → Install Plugin from Disk...** 安裝。
- **透過命令列安裝**：

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(請依使用的 IDE 將 `idea` 替換為 `goland`、`pycharm`、`webstorm`、`rustrover` 或 `clion`。)*

## 從原始碼建置

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

可安裝的封裝檔會輸出至 `build/distributions/`。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 53 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
