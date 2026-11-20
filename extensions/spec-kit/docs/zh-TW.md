# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

一個 [Spec Kit](https://github.com/github/spec-kit) 擴充套件。在實作功能前，`/speckit.deja-vu.recall` 會檢索過往的程式設計代理會話紀錄，整理先前的嘗試、已定決策與失敗路線，並在每一點附上對應的會話 ID。

會話資料來自 [deja](https://github.com/vshulcz/deja-vu)，它會索引 Claude Code、Codex、Cursor、Copilot、Gemini 等代理已寫入磁碟的紀錄。所有資料皆留在本機。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安裝

請先安裝 `deja`：

```sh
brew install deja-vu
# 或
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

接著在 Spec Kit 專案中加入擴充套件：

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

每個 deja 版本都會附帶對應版本的 `deja-spec-kit.zip`。若想改用本機目錄測試，可執行：`specify extension add --dev /path/to/deja-vu/extensions/spec-kit`。

## 用法

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

不帶參數時，會讀取目前功能的 `spec.md` 與 `plan.md` 並自動挑選檢索詞；帶參數時則直接搜尋指定字詞。指令格式依使用的代理而異，可能顯示為 `/speckit-deja-vu-recall` 或 `$speckit-deja-vu-recall`。

擴充套件底層會在儲存庫根目錄執行：

```sh
deja recall -- "<query>"   # 符合的會話（目前專案優先）
deja files -- "<query>"    # 該主題曾開啟或編輯的檔案
```

當片段過短無法判斷脈絡時，還會呼叫 `deja show <id>`。輸出報告為 `Prior sessions` 區段，分為已嘗試與失敗、已決定、檔案與待辦項目，每點皆標註 `(deja:<id>)`。

## 鉤子（Hook）

本擴充套件註冊一個可選的 `before_implement` 鉤子。執行 `/speckit.implement` 時，代理會詢問是否先進行歷史召回；未經你同意不會自動執行。若要關閉，可在 `.specify/extensions.yml` 將該項目設為 `enabled: false`，或執行 `specify extension disable deja-vu`。

## 說明

- 唯讀操作：此指令僅執行檢索與讀取，不會變更任何檔案。
- 本機首次呼叫 deja 會建立索引，歷史資料龐大時可能需要一點時間；之後每次回應遠低於一秒。
- 召回的會話文字僅作為參考資料；指令會指示代理切勿照著紀錄中的文字下達指令執行。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 50 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

## 授權條款

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
