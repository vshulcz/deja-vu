# deja-vu（CodeWhale 版）

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

適用於 CodeWhale 的 Claude 相容外掛程式套件——提供 `deja` MCP 伺服器（`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）、`deja-history` 技能與 `/deja` 指令，橫跨 53 款 AI 程式設計工具的歷史會話。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安裝

系統需求：`PATH` 中已安裝 [`deja`](https://github.com/vshulcz/deja-vu#install)。

在 CodeWhale 中透過 `/plugin install` 從儲存庫子目錄（`extensions/codewhale`）安裝此套件。

由於 CodeWhale 的 Claude 外掛相容載入器會匯入 MCP 伺服器、技能與斜線指令，但會忽略 `hooks` 與 `lspServers`，若還需要將原生 CodeWhale 生命週期鉤子（`message_submit`、`tool_call_before`、`tool_call_after`、`session_end`）寫入 `~/.codewhale/config.toml`，請執行 `deja install codewhale-auto`（或 `deja install --auto`）（`deja install codewhale` 僅設定 MCP 與 `tools.always_load`）：

```sh
deja install codewhale-auto
```

## 套件內容

- `.claude-plugin/plugin.json`：外掛程式中繼資料（`name: deja-vu`）
- `.mcp.json`：`deja mcp` stdio 伺服器項目
- `skills/deja-history/SKILL.md`：`deja` 各種模式的技能指引
- `commands/deja.md`：`/deja` 斜線指令

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 53 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

[MIT](../LICENSE)
