# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 個會話 · token 用量為 agentmemory 的**一半**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 給星](https://github.com/vshulcz/deja-vu)**

一套 Hermes 記憶提供者（memory provider），直接從本機既有的程式設計會話回答問題：涵蓋 Claude Code、Codex、Cursor、Hermes 本身以及 deja 可讀取的其他代理，連同安裝任何工具之前數月留下的紀錄。無須額外模型、API 金鑰或常駐服務；歷史紀錄由單一 Go 執行檔 [deja](https://github.com/vshulcz/deja-vu) 在本機建立索引。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安裝

需先確保 `PATH` 上可執行 `deja`：

```bash
brew install deja-vu        # 或: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # 串接 Hermes 與本機所有支援的代理
hermes plugins install deja-memory   # 或從 GitHub: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

或在 `hermes memory setup` 中選擇 `deja-memory`，它會在啟用前先檢查執行檔是否存在。

## 運作方式

- **會話首輪前**：提供與 deja 鉤子給 Claude Code、Codex 相同的專案摘要——本專案近期工作與已確認決策。之後僅在提問剛好對應過往內容時才附加召回，多數輪次不會多加任何訊息。
- **清楚可見的引用來源**：deja 在 Claude Code 顯示的提示（來自哪個代理的記憶回執、會話啟動召回、首次建置與每週摘要）會在互動式 CLI 與 `hermes chat -q` 中透過 Hermes 的召回列 `🧠 deja · …` 呈現，模型回覆本文維持原樣。Hermes 0.17 沒有召回列，`hermes -z` 與 `hermes chat -Q` 不會輸出，TUI 僅在狀態列閃過，閘道對話也可能是其他人看得到的群組，因此在這些情境下 deja 會保留紀錄至可顯示的會話再呈現。
- **排程任務（`cron`）不自動注入**：當 Hermes 回報平台為 `cron` 時，提供者不會主動加入上下文；若任務自行呼叫 deja 工具仍會正常作答。
- **三項工具**：`deja_recall` 搜尋歷史會話、`deja_fix` 回報上次相同錯誤後執行的修復、`deja_blame` 列出編輯過該檔案的會話。
- **筆記優先權**：Hermes 寫入 `MEMORY.md` 或 `USER.md` 的條目也會保留為 deja 筆記，排序時高於鄰近會話。
- **壓縮銜接**：壓縮（compaction）時會記住會話正進行到哪裡，供下一輪接續；會話結束時移除活躍標記。

記憶提供者本身有一項限制：無法把先前修復直接插在剛失敗的指令旁。這需透過外掛鉤子 `transform_tool_result`，而 Hermes 傳給記憶提供者的上下文中 `register_hook` 沒有動作（`plugins/memory/__init__.py`，0.17.0）。執行 `deja install hermes-auto` 會安裝帶入該鉤子的外掛程式。

本外掛從不重複抄寫 Hermes 對話：Hermes 已將它們寫入儲存區，deja 會在下次更新時直接讀取。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1；MemPalace 公布 96.6%，agentmemory 為 95.2%） | **token 減少 58%**（同儲存庫重複任務上 token 用量只有 agentmemory 的**一半**） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 48 個程式設計代理的檢索僅 **0.7 ms**（7 款對比工具中第二名 72 秒） |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 與 `deja install` 同源程式碼

`deja install hermes-auto` 會將這份提供者寫入 `~/.hermes/plugins/deja-memory`，同時安裝 MCP 伺服器與 `pre_llm_call` 鉤子外掛。這裡的檔案與安裝器源自同一份原始碼（`cmd/deja/install_hermes_memory.go`），唯一差異是這份複本改由 `PATH` 尋找 `deja`，而非寫死路徑。若兩者不一致，`TestHermesExtensionIsTheProviderInstallWrites` 測試便會失敗。

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。覺得實用？[到 GitHub 為 deja-vu 點顆星](https://github.com/vshulcz/deja-vu)。
