# deja for Zed

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) 索引程式設計代理寫入磁碟的會話檔案——包含 Claude Code、Codex、Cursor、opencode 與其他 45 款工具——並依此作答。本擴充套件將該索引作為 context server 提供給 Zed 代理面板，使對話可檢索你先前的實作紀錄，包括在安裝 deja 之前留下的工作內容。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 代理可呼叫的工具

單一工具 `deja`，透過 `mode` 參數呼叫：

| Mode | 回答內容 |
|---|---|
| `recall` | 符合錯誤字串、函式名稱、檔案路徑或參數旗標的會話。 |
| `context` | 當決策推論很重要時，取得單一歷史會話的完整摘要。 |
| `blame` | 在編輯或刪除檔案前，找出曾討論過該檔案的會話。 |
| `fix` | 先前在這台機器遭遇相同錯誤後執行的修復指令。 |
| `how` | 依據本機實際執行紀錄，還原建置、測試或部署指令。 |
| `orient` | 過去會話在此專案執行過的指令與編輯過的檔案。 |
| `remember` | 儲存一條長期專案決策以便日後召回。 |
| `handoff` | 取得另一會話的狀態，從中止處銜接繼續。 |

## 安裝

從 Zed 的擴充套件清單安裝。首次使用時會請 Zed 執行 `deja --version`，若成功則直接呼叫 Zed `PATH` 上的 `deja`；否則會下載釋出版本的 `deja` 執行檔到擴充套件目錄。因擴充套件在沙盒中執行、無法自行檢查常見安裝路徑，若 `deja` 不在啟動 Zed 時的 `PATH` 中，必須明確指定路徑：

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` 也會設定 Zed，並直接寫入 `settings.json`——若你已有 CLI，這是最快的方式。兩者共用相同 ID `deja-context-server`，而 Zed 以 ID 識別伺服器，因此無論安裝順序如何都只會有一個伺服器設定：安裝程式偵測到擴充套件設定會予以保留，之後再裝擴充套件也會對應同一組鍵值。執行 `deja uninstall zed` 可移除 CLI 寫入的項目。

索引與搜尋全在本機執行：沒有網路連線呼叫，建立索引時亦會自動遮罩機密與憑證。

## 基準評測

| 召回率 | Token 用量 | 索引與檢索耗時 |
|---|---|---|
| LongMemEval-S **97.2% R@5**（470 題清洗後測得 88.9% hit@1） | **token 減少 58%**（同儲存庫重複任務） | **13.6 秒**索引 19,195 個歷史會話 / 跨越 49 個程式設計代理的檢索僅 **0.7 ms** |

## 儲存庫記憶與跨代理接力

透過 git 在 `.deja/memory.jsonl` 中共享經過篩選的專案決策與踩坑紀錄（`deja repo candidates`、`deja repo add <id>`）。條目在寫入前自動脫敏，依內容雜湊排序並附帶 `merge=union`，平行分支可無衝突合併；clone 儲存庫的隊友皆會自動載入（在信任原則設定 `{"activations":{"auto":{"repo":false}}}` 可停用 hook 注入）。[了解更多](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

任務進行中需要切換工具？執行 `deja handoff --to <agent>`（或透過 MCP 呼叫 `handoff`），即可將目前會話的目標、已定決策、異動檔案、通過的檢查與未完成工作直接接力給另一個程式設計代理。

MIT 授權，與 deja 相同。

[deja-vu](https://github.com/vshulcz/deja-vu) 生態組件。MIT 授權。
