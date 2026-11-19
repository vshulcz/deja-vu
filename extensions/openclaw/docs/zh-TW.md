# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

將同機其他 48 款程式設計智慧體（Claude Code、Codex、Cursor、Gemini、Zed 等）的對話歷史接入 [OpenClaw](https://openclaw.ai)。完全以單一 Go 執行檔 `deja` 與本地 BM25 索引在本地執行，不需額外 LLM 或向量模型（設定 `DEJA_OFFLINE=1` 可停止每日檢查）。

## 安裝

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

建議先或一併安裝系統版 `deja`：

```sh
brew install deja-vu
```

若本機已由 `deja install openclaw-auto` 或 `deja install --auto` 安裝原生套件，外掛會自動讓位，不會重複註冊或重覆注入。

## 功能特色

- **`agent:bootstrap`（支援 `openclaw agent --local`）**：在開啟工作階段時帶入該專案既定背景與結論。
- **`before_prompt_build`（需 OpenClaw `2026.8.1` 與 `hooks.allowConversationAccess: true`）**：每輪回覆前依使用者提示檢索相關經驗。
- **`before_compaction`**：於對話壓縮摘要前保留目前任務目標、結論與執行指令狀態。
- **`session_end`**：寫入工作階段紀錄並更新索引。
- **工具執行中介層**：當指令報錯或讀寫關鍵檔案時，主動帶出過去類似錯誤的解法與檔案註記。
- **`deja_recall` / `deja_fix` / `deja_blame` 工具**：提供智慧體主動呼叫歷史搜尋、修復命令查詢與檔案追溯。
- **`/deja <query>` 指令**：直接在對話中依關鍵字搜尋全部已索引智慧體的紀錄。

### 私訊來源回執（DM receipt）

在符合條件的私訊回應末端，擴充套件會透過 `reply_payload_sending` 附上一行斜體來源標註，例如：

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

回執必須同時符合下列四個條件才會顯示：

1. 非群組私聊（`isGroup: false` / `direct`）。
2. 發送者位於擁有者允許清冊中（`commands.ownerAllowFrom` / `allowFrom`）。
3. 未啟用區塊串流傳送（`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`）。
4. 為送往私訊對方的最終文字回覆（`direct`）。

若不需要顯示此標註，設定 `"receipt": false` 即可停用。

## 設定範例

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

亦可直接使用命令列開啟存取設定：

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## 使用哪個執行檔

解析順序：`bin` 設定 -> `DEJA_BIN` -> `PATH` -> 常見系統安裝位置 -> `@vshulcz/deja-vu` 內附複本。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 49 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 隱私

索引位於 `~/.cache/deja/index.db`（可用 `DEJA_INDEX_DIR` 調整），並於寫入時遮罩 `sk-*`、`ghp_*`、`AKIA*` 與 `Bearer` 金鑰。除非明確設定並執行 `deja embed`，否則全程離線運作。使用 `deja install --auto` 可為本機 49 款智慧體統一啟用記憶整合。

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
