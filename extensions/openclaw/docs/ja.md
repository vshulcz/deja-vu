# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub でスター](https://github.com/vshulcz/deja-vu)**

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

同じマシン上の他 47 種類のコーディングエージェント（Claude Code・Codex・Cursor・Gemini・Zed など）の履歴を [OpenClaw](https://openclaw.ai) へ接続するプラグインです。単一 Go バイナリ `deja` とローカル BM25 検索で完結し、LLM やクラウド API は使いません（`DEJA_OFFLINE=1` で日次リリースチェックも無効化できます）。

## インストール

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

本体バイナリの導入（推奨）：

```sh
brew install deja-vu
```

`deja install openclaw-auto` や `deja install --auto` で既にネイティブ構成が入っている場合は自動で待機し、重複注入を防ぎます。

## 動作内容

- **`agent:bootstrap`（`openclaw agent --local` 対応）**：起動時に当該プロジェクトの過去の確定事項をロードします。
- **`before_prompt_build`（OpenClaw `2026.8.1` および `hooks.allowConversationAccess: true` 対応）**：毎回の入力に合わせて関連履歴を注入します。
- **`before_compaction`**：コンテキスト圧縮前に進行中タスク・結論・未完了コマンドを退避・再提示します。
- **`session_end`**：セッション終了時にローカルインデックスへ反映します。
- **ツール結果ミドルウェア**：コマンド失敗や主要ファイル編集時に過去の対策や背景判断を添えます。
- **`deja_recall` / `deja_fix` / `deja_blame` ツール**：エージェントから明示的に検索・修正コマンド参照・変更元追跡を実行できます。
- **`/deja <query>`**：チャット上から全ツールの履歴をキーワード検索します。

### DM 引用レシート

条件を満たす DM 返信の末尾には `reply_payload_sending` 経由で斜体の出典行が添えられます：

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

次の 4 条件をすべて満たす場合にのみ付与されます：

1. グループではない DM（`isGroup: false` / `direct`）。
2. 送信元がオーナー許可リスト（`commands.ownerAllowFrom` / `allowFrom`）に合致。
3. ブロックストリーミングが無効（`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`）。
4. DM 向けの最終テキスト返信（`direct`）である。

無効にしたい場合は設定で `"receipt": false` を指定します。

## 設定例

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

CLI から設定する場合：

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## バイナリ解決順

`bin` 指定 -> `DEJA_BIN` -> `PATH` -> 標準パス -> `@vshulcz/deja-vu` 同梱バイナリ。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 48 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## プライバシー

インデックスは `~/.cache/deja/index.db`（`DEJA_INDEX_DIR` で変更可）に保管され、`sk-*`、`ghp_*`、`AKIA*`、`Bearer` 等を構築時に自動削除します。明示的な `deja embed` を使わない限り完全ローカルで動作し、`deja install --auto` で 48 ツール全体に一括導入できます。

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
