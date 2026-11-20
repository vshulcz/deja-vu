# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) 索引程式設計代理已經寫入磁碟的會話檔案——包含 Claude Code、Codex、Cursor、opencode 與其他 45 款工具——並依此作答。此擴充外掛將該索引帶入 Kimi Code：每次提問皆會夾帶相關回憶，代理也能主動搜尋歷史紀錄。

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 安裝

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

此指令格式會釘選最新發行版本並紀錄來源，供 Kimi 檢查更新。Release 頁面亦附有獨立資產 `kimi-deja.zip`（約 10 KB，不需拉取整座儲存庫），適合市集上架或輕量安裝。

若尚未安裝執行檔，請先安裝 `deja`（也可順手串接本機其他代理）：

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

外掛由受管複本載入，安裝後請執行 `/reload` 或 `/new`。

## 更新

Kimi 只會對來自官方市集的外掛通知更新。若透過儲存庫安裝，再次執行 `/plugins install` 即可拉取最新版；當已裝複本落後於目前 `deja` 內建版本時，`deja doctor` 會顯示提示：

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## 內含功能

- **與 `deja install kimi-auto` 相同的鉤子**：會話第一則提問帶入此專案已定事項（`deja hook-context --once`）；每次提問執行相關性檢索（`deja hook-prompt`），Kimi 將結果附加到該輪對話，無符合項目則不附帶任何訊息；壓縮前先讀取即將摺疊的對話輪次，讓下一則提問接續原本進行中的任務（`deja hook-precompact`）；在 `SessionEnd` 移除活躍會話標記（`deja hook-session-end`）。
- **工具**：宣告 `deja mcp` 為 MCP 伺服器——提供單一工具 `deja`，支援 `recall`、`context`、`blame`、`fix`、`how`、`orient`、`remember` 或 `handoff` 模式。
- **`/deja:recall <query>`**：供你主動搜尋過往歷史。
- **`deja-history` 技能**：會話開始即載入，提醒代理在重頭除錯前先查詢紀錄。

## 執行檔尋找順序

依序為：`DEJA_BIN`、你自行安裝的 `deja`（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin`），最後才是 `PATH` 中的 `deja`。你透過 `deja update` 或 `brew upgrade deja-vu` 更新的版本永遠優先於外掛版本鎖定的副本。

## 若你也曾執行 `deja install kimi`

CLI 會在 `~/.kimi-code/mcp.json` 寫入同一個 MCP 伺服器並在 `config.toml` 加入相同鉤子。由於 Kimi 會把外掛伺服器掛在專屬命名空間（`plugin-deja:deja`），若同時啟用會導致工具重複出現兩次、召回附加兩次。

此外掛會在每次鉤子觸發與伺服器啟動時檢查 CLI 安裝器是否已設定；一旦偵測到便自動讓位，由 `deja install` 維護的版本負責。執行 `deja uninstall kimi` 後不需重裝即可無縫還回給外掛。

若僅由外掛接線，`deja doctor` 會在 kimi 旁標示 `plugin`，不會誤報未設定。

有兩項例外無法偵測：你手動新增且名稱非 `deja` 的 MCP 項目（會並行運作），以及你自撰且不含安裝器標記註解、直接呼叫 deja 的鉤子。

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
