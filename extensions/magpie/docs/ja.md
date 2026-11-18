# `@vshulcz/magpie-deja` — deja-vu 用 Magpie ゲートウェイフック

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> [Magpie](https://github.com/yetone/magpie) を通るリクエストに対し、同じマシン上の他 47 種類のコーディングエージェント（Claude Code、Codex、Cursor、Gemini CLI、OpenClaw、Hermes など）が保存した履歴記憶を自動補完します。

OpenAI（`chat`・`responses`）・Anthropic（`anthropic`）・Gemini（`gemini`）の各ゲートウェイプロトコルに対応し、単一 Go バイナリの `deja` がローカル BM25 検索を実行します。追加の LLM や埋め込みモデルは不要です（`DEJA_OFFLINE=1` で日次バージョン確認も停止可能）。

## インストール

```sh
magpie plugin add @vshulcz/magpie-deja
```

実行環境に `deja` バイナリ（`@vshulcz/deja-vu`、`brew install deja-vu`、または `install.sh`）と `deja index` で作成されたローカルインデックスが必要です。48 エージェントすべてに MCP ツール（`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）を導入する場合：

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

メッセージ内に `<deja-memory>` または `<deja-recall>` が既にある場合は二重挿入をスキップします。

## 動作仕様とデータ境界

1. **`onRequest` が `deja hook-prompt --plain` を実行**：`~/.cache/deja/index.db`（`DEJA_INDEX_DIR` 指定可）を `800ms` タイムアウト以内で参照し、一致時に `<deja-memory>` を付加します。**外部送信に関する注意**：注入された履歴はそのまま上流 LLM プロバイダへの送信リクエストに含まれます。
2. **厳密なプロジェクトスコープ**：`ctx.cwd` を使用し（`allowUnscoped: false`）、ゲートウェイ自体の `process.cwd()` は `deja hook-prompt` へ渡しません。
3. **ローカル起点ゲート**：`recallRemote` は既定で `false` であり、`ctx.origin === "local"` または `ctx.origin === "magpie-key"` のみ対象になります。
4. **二層マスキング**：インデックス作成時および送出前に `deja.hooks.mjs` と同じ `maskSecrets` で `sk-*`、`ghp_*`、`github_pat_*`、`AKIA*`、`Bearer`、`PRIVATE KEY`、URL 認証情報、`api_key=...` を除去します。
5. **ループ抑止とキャッシュ維持**：`<deja-memory`・`<deja-recall>`・`[deja —` が含まれる場合は再挿入せず、プロンプトプレフィックスキャッシュを壊さないようユーザーメッセージ側のみ更新します。

## 設定オプション

| オプション | 既定値 | 説明 |
|---|---|---|
| `requestTimeoutMs` | `800` | `deja` 呼び出しのタイムアウト（`50`–`5000` ms） |
| `maxBytes` | `4000` | 注入するコンテキストの上限サイズ（`256`–`32768` bytes） |
| `recallRemote` | `false` | ローカル起点以外のリクエストでも記憶を呼び出すか |
| `allowUnscoped` | `false` | `ctx.cwd` 不在時に全体検索を許可するか |
| `cwd` | `""` | リクエストのプロジェクトディレクトリを明示上書き |
| `skipAgents` | `[]` | 二重挿入を防ぐエージェント名の除外リスト（例: `["claude-code"]`） |
| `bin` | `""` | `deja` バイナリへのパス |

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 48 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
