# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

適用於 DeepSeek Harness（`dsh`）與 DSH Desktop（`anywhere-labs/dsh-desktop`）的 `session-query` 外掛。除了 DSH 自身的對話紀錄，也一併檢索本機其餘 48 款程式設計智慧體（Claude Code、Codex、Cursor、Gemini、Hermes 等）留在磁碟上的工作階段歷史，全部經由本地單一 Go 執行檔 `deja` 與 BM25 索引執行（`DEJA_OFFLINE=1` 可關閉每日版本檢查）。

## 安裝

安裝時必須指定 `--profile`（例如 `web`）：

```sh
dsh plugin --profile web add dsh-deja
# 或直接從儲存庫安裝：
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

若系統尚未安裝 `deja`，可透過 `DEJA_BIN` 指定路徑，或執行以下指令一併完成全機 49 款智慧體與 `$DSH_HOME/plugins/deja/` 之設定：

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## 外掛提供內容

### 六項智慧體工具

| 工具 | 用途 |
|---|---|
| `deja_recall` | 依關鍵字、錯誤訊息或設定旗標檢索過往工作階段 |
| `deja_session` | 取得符合片段前後的完整對話內容 |
| `deja_blame` | 追查某個檔案或 `path:line` 源自哪一次工作階段 |
| `deja_fix` | 查詢上次解決同類錯誤時採用的指令 |
| `deja_how` | 檢視本專案過去常用的指令與參數 |
| `deja_remember` | 寫下一條已確定的結論或規範 |

- **`/deja <...>` 指令**：在輸入欄直接以條件式搜尋歷史對話。
- **整合執行模式**：支援 `deja install dsh-auto` 與 `dsh web`（含 `tui`、`headless`、`sdk`）；若要關閉自動注入，在 YAML 設定檔中設為 `autoRecall: false` 即可。

## 搜尋快速鍵與面板操作

前往 `Settings → Deja` 可管理全部互動控制：

- **Mod+F**（或 **Mod+Alt+F** / macOS 上為 **Cmd+Option+F**）：開啟搜尋列；可用 `/deja --session <bare session UUID> -- <your draft>` 將搜尋範圍限縮在特定工作階段。
- **Mod+Shift+K**（macOS 為 **Cmd+Shift+K**）：開啟全域跨工具搜尋。
- **Mod+K**：喚出面板並支援切換 **DSH conversations only**（單次最多顯示 20 筆結果）。
- **Linux 瀏覽器備用快速鍵**：為避開系統快捷鍵衝突，額外支援 **`Mod+/`**、**`Mod+Shift+,`**、**`Mod+Shift+.`**（設定鍵為 `shortcuts.open`）。

## 自訂快速鍵

在 `Settings → Deja` 中找到 **`session.search`**，可使用 **Change shortcut** 修改按鍵、**Restore default** 還原預設或 **Remove** 移除設定。畫面由 **dsh-deja package host plugin** 提供的 `/api/deja-search` 驅動；呼叫 `/api/deja-search?probe=1` 若回應 `{"ok": true, "plugin": "dsh-deja"}`，表示與本地 `deja search` 連線正常。

## 內部接線方式

- **系統提示鉤子**：透過 `ctx.systemPrompt.context` 與 `agent/pre-step`（支援 `dsh 0.1.1-rc.2`+）送出 `additionalContext` 與 `systemMessage`。
- **API 端點與限制**：`/api/deja-notes` 透過 `--plain` 提供筆記給 `client.js`；`GET /api/deja-search` 呼叫 `deja search --harness deepseek --json --limit 20 -- <query>`，具備 20 秒逾時、2 MiB 輸出上限與 500 字元查詢限制。
- **側邊欄掛載**：掛載於 `data-slot="sidebar.workspaces"`，使用 `aria-expanded` 與 `_listArea`（相容 DSH `0.2.1-rc.5`）。

## 測試搜尋元件

```sh
node --test extensions/dsh/test/*.test.js
```

免建置的瀏覽器互動測試頁面位於 `extensions/dsh/test/search-browser.html`（使用 React 18 CDN）。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 49 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 不會執行的操作

本地索引存放於 `~/.cache/deja/index.db`（可透過 `DEJA_INDEX_DIR` 調整），除非主動執行 `deja embed` 否則完全離線。建立索引時會遮罩 `sk-*`、`ghp_*`、`AKIA*` 與 `Bearer` 憑證，且絕不修改原始工作階段記錄。

## 授權條款

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
