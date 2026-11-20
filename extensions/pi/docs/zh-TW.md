# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

為 [pi](https://github.com/badlogic/pi-mono) 接入跨越本機 48 款其他程式設計智慧體（Claude Code、Codex、Cursor、Gemini、OpenClaw、Hermes 等）工作階段歷史的本地記憶擴充套件。底層使用單一 Go 執行檔 `deja` 建立本地 BM25 索引，不依賴外部 LLM、向量模型或雲端帳號；除了主動執行的更新或同步指令，以及可用 `DEJA_OFFLINE=1` 停用的每日版本檢查外，不會發起任何網路連線。

## 安裝

```sh
pi install npm:@vshulcz/pi-deja
```

如果本機尚未預先安裝 `deja`，擴充套件會依[使用哪個執行檔](#使用哪個執行檔)中的順序改用隨套件附帶的版本；你也可以手動安裝系統版執行檔：

```sh
brew install deja-vu
```

若本機已經執行過 `deja install pi-auto` 或 `deja install --auto` 安裝了獨立擴充套件，這個 npm 套件會自動退讓，避免在同一個工作階段中重複註冊或雙倍注入。這套基於 `pi.extensions` 與 `before_agent_start` 的機制也適用於 `omp`。

## 擴充套件功能

- **工作階段啟動（頁尾狀態列）**：開啟工作區時在底部狀態列顯示已索引的工作階段數與命中數。
- **每次提問（`before_agent_start`）**：根據目前提示詞檢索此專案過去的決策與排查紀錄並自動注入上下文。
- **指令失敗後（`bash` / `powershell` 的 `tool_result`）**：當命令傳回非零狀態碼時，附帶歷史上同類錯誤之後實際成功執行的修復指令。
- **編輯與讀取時（read/edit/write 的 `tool_result`）**：協助在讀寫關鍵檔案時看到先前的設計脈絡與注意項目。
- **壓縮摘要後（`session_compact`）**：在上下文壓縮後重新補回當前的任務目標、定論與未完成命令清單。
- **工作階段結束（`session_shutdown`）**：更新此工作階段在本地索引中的紀錄。
- **`/deja <query>` 指令**：直接在對話列用關鍵字搜尋全部已索引智慧體的工作階段。

## 使用哪個執行檔

擴充套件會依照下列順序尋找 `deja` 執行檔：

1. 環境變數 `DEJA_BIN` 指定的路徑
2. `PATH` 中可用的 `deja`
3. 常見本機安裝路徑（`~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin` 等）
4. npm 套件 `@vshulcz/deja-vu` 隨附的複製

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 50 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 隱私與安全

索引位於 `~/.cache/deja/index.db`（可用 `DEJA_INDEX_DIR` 自訂），寫入索引前會自動遮罩 `sk-*`、`ghp_*`、`AKIA*` 與 `Bearer` 等金鑰。檢索完全在本地離線運作，唯有主動執行 `deja embed` 並設定向量端點時才會向外傳送資料。透過 `deja install --auto` 可在 50 款程式設計智慧體中統一啟用 `recall`、`fix`、`blame` 與 `handoff`。

## 授權條款

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
