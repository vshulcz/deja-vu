# deja-vu（Orca 版）

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

適用於 `deja` 的 Orca v2 `trusted-worker` 外掛程式——將 49 款 AI 程式設計工具（Claude Code、Codex、Cursor、opencode、Gemini CLI、Orca 等）的會話記憶帶入 Orca，包含安裝 `deja` 之前早已留存的歷史會話。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安裝

系統需求：`PATH` 中（或 `~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin`、`~/bin`、`%LOCALAPPDATA%\Programs\deja` 目錄下）已安裝 [`deja`](https://github.com/vshulcz/deja-vu#install)。

在 Orca（`>=1.4.248`）中載入外掛程式目錄 `extensions/orca`。

若還想在 Orca 內啟用原生 MCP、skill 檔案、`SessionStart`、`PreCompact`、`SessionEnd` 與指令失敗修復提示，請同步執行：

```sh
deja install orca-auto
```

（`deja install orca` 僅安裝 MCP、skill 與 `/deja`，不含生命週期鉤子；`deja install orca-auto` 或 `deja install --auto` 會安裝完整鉤子組合。）

當 `deja install orca-auto` 已經把原生 `UserPromptSubmit` 鉤子寫入 Orca 的 `settings.json` 時，此 worker 會自動退讓，不會重複注入 `<deja-recall>` 內容。

## 外掛程式功能項目

- **Hooks**
  - `agent.prompt.before`：解析目前工作區路徑，並在 Orca 2 秒鉤子時間預算內（`1.3s` 逾時，失敗即放行）將相關 `<deja-recall>` 上下文注入提示詞。
  - `agent.tool.before`：僅在啟動 `Agent` 或 `Task` 子代理時將 `<deja-recall>` 附加至 `input.prompt`（絕不改寫 `Bash`、`Read`、`Edit` 等其他工具輸入）。
  - `agent.tool.after`：在 Orca 宿主 API 屬純觀察鉤子（會忽略 `next` 且不等待），因此失敗指令的 `fix` 交付由 `deja install orca-auto` 負責。
- **Commands**
  - `deja.orient`（`deja: Orient on workspace`）：針對目前工作區執行 `deja hook-context --plain` 並顯示摘要通知。
  - `deja.recall`（`deja: Recall past sessions`）：執行 `deja search --limit 5 -- <query>`（未輸入查詢時執行 `deja last -n 5`）並顯示結果。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 49 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

[MIT](../LICENSE)
