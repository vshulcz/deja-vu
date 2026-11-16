# opencode-deja

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 個會話 · token 用量為 agentmemory 的**一半**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 給星](https://github.com/vshulcz/deja-vu)**

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

為 [opencode](https://opencode.ai) 帶來同機另外 47 款程式設計智慧體（Claude Code、Codex、Cursor、Gemini、Hermes、Zed 等）的工作階段記憶。基於單一 Go 執行檔 `deja` 與本地 BM25 索引，不需要外部 LLM；設為 `DEJA_OFFLINE=1` 可停用每日更新檢查。

## 安裝

加入 `opencode.ai/config.json`：

```json
{ "plugin": ["opencode-deja"] }
```

（opencode 2.x 請改用 `"plugins": ["opencode-deja"]` 或執行 `opencode plugin add opencode-deja`。）也可直接使用 `deja install --auto` 在 `opencode.json` 與 `plugins/deja.js` 寫入設定。

## 提供內容

### 六項智慧體工具

| 工具 | 說明 |
|---|---|
| `deja_recall` | 依關鍵字或錯誤訊息搜尋過去的工作階段 |
| `deja_session` | 讀取指定工作階段片段的完整脈絡 |
| `deja_blame` | 追溯某個檔案或 `path:line` 當初在何處寫入 |
| `deja_fix` | 查詢上次修好相同報錯的實際指令 |
| `deja_how` | 查詢專案常用命令列與測試參數 |
| `deja_remember` | 保存明確定論免去下次重查 |

### 四項自動記憶注入

- **會話啟動提醒**：opencode 1.x 顯示精簡 toast，2.x 直接靜默寫入系統指令，風格如 `deja-vu — from Codex CLI, 4 months ago: "…"`。
- **20 分鐘冷卻並跳過子代理**：同一專案避免重複刷屏，僅在主工作階段注入而不佔用 sub-agent 上下文。
- **命令除錯與檔案歷史**：工具失敗時回傳過往解法，動到關鍵檔案時補充前情。
- **壓縮前保存**：於上下文摘要壓縮時保留正在處理的任務與未完成指令。

## 設定選項

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

（2.x 使用 `"plugins"`）：

- `autoRecall`：是否啟用自動上下文注入（預設 `true`）。
- `tools`：是否註冊六個工具（預設 `true`）。
- `bin`：明確指定 `deja` 執行檔位置。

如需為本機 48 款程式開發工具一次啟用完整 MCP（含 `recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`），請執行 `deja install --auto`（詳見[切換智慧體不遺失上下文](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)）。

## 使用哪個執行檔

搜尋順序：`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin` -> 隨套件附帶之複本。

## 隱私

索引儲存於 `~/.cache/deja/index.db`（可透過 `DEJA_INDEX_DIR` 調整），並在寫入索引前清除 `sk-*`、`ghp_*`、`AKIA*` 與 `Bearer` 金鑰；除非主動設定 `deja embed`，否則所有檢索均在本機離線進行。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 為 95.2%） | **token 減少 58%**（同儲存庫重複任務上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 48 個程式設計代理的檢索僅 **0.7 ms**（7 款對比工具中第二名 72 秒） |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## ## 授權條款

MIT

覺得實用？[到 GitHub 為 deja-vu 點顆星](https://github.com/vshulcz/deja-vu)。
