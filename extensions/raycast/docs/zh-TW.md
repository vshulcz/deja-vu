# deja-vu Raycast 擴充功能

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

從 Raycast 搜尋並繼續 Claude Code、Codex、Cursor、OpenCode、Gemini CLI 與另外 48 款程式設計代理的歷史會話。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 前置需求

安裝 [`deja-vu`](https://github.com/vshulcz/deja-vu)：

```sh
brew install deja-vu && deja install --auto
```

## 指令

- **Search Sessions**：全文搜尋 Mac 上每一筆程式設計代理會話（`deja search --json --limit 25 -- <query>`），將精確吻合與最接近的關聯結果分區呈現。
- **Recent Sessions**：瀏覽所有已安裝代理最近的 100 筆會話（`deja last 100 --json`），附帶中繼資料預覽（`Agent`、`Project`、`Started`、`Updated`、`Session ID`）。

## 動作

- **Show Session**：在 Raycast 內閱讀已脫敏的對話內容（`deja show <id> --json --limit 200`）。
- **Resume in Terminal**：在 `Terminal.app` 中執行該代理的原生效能繼續指令（`deja resume <id>`）。
- **Copy Resume Command**、**Copy Session ID** 與 **Open Project Folder**。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 53 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 本機開發

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
