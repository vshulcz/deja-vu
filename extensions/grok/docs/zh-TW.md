# Grok Build 中的 deja

[English](../README.md) | [简体中文](zh.md) | 繁體中文 | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** 擴充套件（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 個會話 · token 用量為 agentmemory 的**一半**）· [文件](https://vshulcz.github.io/deja-vu/) · [線上展示](https://vshulcz.github.io/deja-vu/view/) · [評測](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub 給星](https://github.com/vshulcz/deja-vu)**

![deja-vu 示範](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

來自你本機工作階段歷史的跨工具記憶——包含 Claude Code、Codex、Cursor、opencode、Zed 以及另外 43 款程式設計智慧體。所有索引與檢索都由單一 Go 執行檔 `deja` 在本地執行（設定 `DEJA_OFFLINE=1` 可停用每日版本檢查）。

## 安裝

先安裝 `deja` 執行檔本體（外掛只包含設定與腳本，不打包原生執行檔）：

```sh
brew install deja-vu
# 或：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

再加入此儲存庫的市集並安裝外掛（官方市集提案：[xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)）：

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

也可以使用互動安裝器將 `[mcp_servers.deja]` 寫入 `~/.grok/config.toml`，並將鉤子寫入 `~/.grok/hooks/deja.json`：

```sh
deja install --auto
```

## 提供的功能

- **`deja` MCP 工具**（8 種模式：`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）
- **`deja-history` 技能（skill）**與 **`/deja:recall <query>`** 指令
- **5 個生命週期鉤子（`hooks/recall.mjs`）**：`SessionStart`、`UserPromptSubmit`、`PreToolUse`（透過 `updatedInput` 補充記錄）、`PostToolUse`（於 Grok 1.0.41 在 `Bash` 執行後提供排障參考）、`PreCompact` 與 `SessionEnd`

## 同時保留兩套安裝也完全相容

當 `~/.grok/config.toml` 已存在 `[mcp_servers.deja]` 或 `~/.grok/hooks/deja.json` 已設定好 deja 鉤子時，外掛會自動禮讓，不會造成重複喚起；若想移除 CLI 設定僅用外掛，執行 `deja uninstall grok` 即可。

## 使用哪個執行檔

搜尋順序：`DEJA_BIN` -> `~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin` -> `PATH`。

## 靜默退讓而不會拖垮工作階段

外掛透過 `${GROK_PLUGIN_ROOT}` 與 `.mcp.json` 掛載。在 Grok Build 1.0.5 及更新版本中，若尚未安裝 `deja` 本體，`grok mcp doctor` 與 `grok inspect` 會見到 `handshake failed: connection closed`，只要安裝 `deja` 就會立刻就緒。

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
