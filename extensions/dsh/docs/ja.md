# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

DeepSeek Harness（`dsh`）および DSH Desktop（`anywhere-labs/dsh-desktop`）向けの `session-query` プラグインです。DSH 自身の会話履歴に加えて、同じマシン上の他 48 種類のコーディングエージェント（Claude Code・Codex・Cursor・Gemini・Hermes など）が保存した履歴を単一 Go バイナリ `deja` とローカル BM25 検索で横断参照します（`DEJA_OFFLINE=1` で日次チェック停止）。

## インストール

プロファイル指定（`--profile`）を付けて導入します：

```sh
dsh plugin --profile web add dsh-deja
# またはリポジトリから直接導入：
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

`deja` 本体がまだない場合は `DEJA_BIN` を指定するか、以下で全 49 エージェントおよび `$DSH_HOME/plugins/deja/` をセットアップします：

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## 追加される機能

### 6 つのエージェントツール

| ツール | 機能 |
|---|---|
| `deja_recall` | エラー文・トピック・フラグ名から過去セッションを検索 |
| `deja_session` | ヒット箇所前後の会話ターンを取得 |
| `deja_blame` | 指定ファイルや `path:line` を扱ったセッションを特定 |
| `deja_fix` | 過去に同じエラーを解消したコマンドを返す |
| `deja_how` | このリポジトリで使われたコマンド・オプションを参照 |
| `deja_remember` | 確定事項をプロジェクトメモとして保存 |

- **`/deja <...>` スラッシュコマンド**：プロンプト欄から即座に履歴検索を実行できます。
- **ホスト連携**：`deja install dsh-auto` や `dsh web`（さらに `tui`・`headless`・`sdk`）と統合され、YAML 設定で `autoRecall: false` にすれば自動注入を無効化できます。

## 検索ショートカットと操作

`Settings → Deja` から検索操作を確認・調整できます：

- **Mod+F**（または **Mod+Alt+F** / macOS は **Cmd+Option+F**）：検索バーを開きます。特定セッションに絞る場合は `/deja --session <bare session UUID> -- <your draft>` を使います。
- **Mod+Shift+K**（macOS **Cmd+Shift+K**）：ワークスペース横断検索を開きます。
- **Mod+K**：パネルを開き、**DSH conversations only** トグルで DSH 履歴のみ（最大 20 件）へ絞り込めます。
- **Linux ブラウザ向け代替キー**：既定ショートカットがブラウザと競合する場合は **`Mod+/`**・**`Mod+Shift+,`**・**`Mod+Shift+.`**（`shortcuts.open` 設定）を利用できます。

## ショートカットのカスタマイズ

`Settings → Deja` の **`session.search`** 行から **Change shortcut** で変更、**Restore default** で初期化、**Remove** で解除できます。バックエンドは **dsh-deja package host plugin** が `/api/deja-search` を提供し、`/api/deja-search?probe=1` が `{"ok": true, "plugin": "dsh-deja"}` を返せばローカルの `deja search` との接続は正常です。

## 内部実装

- **フック経路**：`ctx.systemPrompt.context` と `agent/pre-step`（`dsh 0.1.1-rc.2` 以降）から `additionalContext` と `systemMessage` を渡します。
- **ルートと制限**：`/api/deja-notes`（内部で `--plain` を使用）を `client.js` が参照し、`GET /api/deja-search` は `deja search --harness deepseek --json --limit 20 -- <query>` をタイムアウト 20 秒・出力上限 2 MiB・クエリ上限 500 文字で実行します。
- **サイドバー統合**：`data-slot="sidebar.workspaces"`、`aria-expanded`、`_listArea`（DSH `0.2.1-rc.5` 対応）を使用します。

## 検索 UI のテスト

```sh
node --test extensions/dsh/test/*.test.js
```

ビルド不要のブラウザ確認ページ `extensions/dsh/test/search-browser.html`（React 18 CDN）も同梱しています。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 49 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## 行わないこと

インデックスは `~/.cache/deja/index.db`（`DEJA_INDEX_DIR` で変更可能）のみに保存され、明示的に `deja embed` を動かさない限り外部送信は一切行いません。`sk-*`・`ghp_*`・`AKIA*`・`Bearer` はインデックス時に自動除去され、元のセッションログを変更することもありません。

## ライセンス

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
