# `@vshulcz/magpie-deja` — deja-vu 的 Magpie 閘道器鉤子

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=social)](https://github.com/vshulcz/deja-vu)

**[★ vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> 為經由 [Magpie](https://github.com/yetone/magpie) 轉發的所有對話請求，自動注入來自本機 48 款其他程式設計智慧體（Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes 等）的跨智慧體歷史記憶。

底層呼叫單一 Go 執行檔 `deja` 與本地 BM25 索引，涵蓋 Magpie 的 OpenAI（`chat`、`responses`）、Anthropic（`anthropic`）與 Gemini（`gemini`）協定路由，不需要額外模型；除明確執行的指令與可用 `DEJA_OFFLINE=1` 停用的每日版本檢查外，擴充套件本身不會發起網路請求。

## 安裝

```sh
magpie plugin add @vshulcz/magpie-deja
```

外掛需要系統中存在可執行的 `deja`（透過 `@vshulcz/deja-vu`、`brew install deja-vu` 或 `install.sh`）並以 `deja index` 建立本地索引。若要同時為本機全部 49 款程式設計智慧體安裝原生 `recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember` 與 `handoff` 工具：

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

當請求內容已經帶著 `<deja-memory>` 或 `<deja-recall>` 時，鉤子會略過重複注入。

## 運作方式與資料邊界

1. **`onRequest` 執行 `deja hook-prompt --plain`**：查詢 `~/.cache/deja/index.db`（可用 `DEJA_INDEX_DIR` 覆寫），預設 `800ms` 逾時即直接放行，命中時寫入 `<deja-memory>` 區段。**對外請求提醒**：被注入的記憶片段會隨該次閘道器請求傳送至上游 LLM 供應商。
2. **工作目錄作用域**：預設使用 `ctx.cwd` 限縮專案範圍（`allowUnscoped: false`），絕不誤用閘道器自身的 `process.cwd()` 傳給 `deja hook-prompt`。
3. **本機來源限制**：`recallRemote` 預設為 `false`，只在 `ctx.origin === "local"` 或 `ctx.origin === "magpie-key"` 時啟用。
4. **雙層遮罩機密**：建索引與送出前皆會透過與 `deja.hooks.mjs` 一致的 `maskSecrets` 規則遮蔽 `sk-*`、`ghp_*`、`github_pat_*`、`AKIA*`、`Bearer`、`PRIVATE KEY`、URL 認證資訊與 `api_key=...`。
5. **防止循環與保護前綴快取**：發現 `<deja-memory`、`<deja-recall>` 或 `[deja —` 時不重複處理，且只加在請求末端而不破壞提示詞前綴快取。

## 設定選項

| 選項 | 預設值 | 說明 |
|---|---|---|
| `requestTimeoutMs` | `800` | `deja` 查詢逾時時間（允許範圍 `50`–`5000` 毫秒） |
| `maxBytes` | `4000` | 注入上下文的最大位元組數（`256`–`32768`） |
| `recallRemote` | `false` | 是否對遠端請求啟用歷史回憶 |
| `allowUnscoped` | `false` | 未指定 `ctx.cwd` 時是否允許全機查詢 |
| `cwd` | `""` | 強制指定用於查詢的專案路徑 |
| `skipAgents` | `[]` | 停用注入的來源代理清冊（如 `["claude-code"]`） |
| `bin` | `""` | `deja` 執行檔的自訂路徑 |

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 49 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
