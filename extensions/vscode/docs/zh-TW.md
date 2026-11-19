# deja-vu（VS Code、Cursor、Windsurf 與 Antigravity 擴充功能）

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**讓你的程式設計代理不再重複除錯已經修好的問題。**

[deja-vu](https://github.com/vshulcz/deja-vu) 本機索引 Claude Code、Cursor、Codex、Windsurf、Copilot Chat、OpenCode、Cline、Roo Code 與其他 41 個程式設計代理寫入磁碟的會話紀錄——單個 Go 二進位檔本機執行，無需 LLM 也無背景常駐程式。

**[說明文件](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[基準評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 擴充功能用途

在 IDE 狀態列即時顯示 `deja statusline` 統計資訊（跨代理索引會話數、今日記憶喚回次數與工作區歷史）。若尚未安裝 `deja`，點擊狀態列項目會在終端機執行 `npx -y @vshulcz/deja-vu install --auto` 自動接線本機偵測到的所有代理。

## 快速安裝

```sh
brew install deja-vu && deja install --auto
# 或不使用 Homebrew：
npx -y @vshulcz/deja-vu install --auto
```

透過命令列安裝狀態列擴充功能：

```sh
code --install-extension vshulcz.deja-status
```

## 實測效果

| 指標 | 結果 |
|---|---|
| LongMemEval-S 檢索準確率（470 道清洗後題目） | **97.2% R@5**，**88.9% hit@1** |
| 重複儲存庫任務的 Token 消耗 | **減少 58%**|
| 行程內詞法查詢延遲 | 跨 49 個程式設計代理中位數 **0.7–0.8 ms** |
| 對 19,195 個歷史會話的零配置建庫耗時 | **13.6 s**，無需 API Key |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
