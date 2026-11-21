# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[pi](https://github.com/badlogic/pi-mono) 向けに、同じマシン上の他 48 種類のコーディングエージェント（Claude Code、Codex、Cursor、Gemini、OpenClaw、Hermes など）のセッション履歴を共有記憶として接続する拡張機能です。単一 Go バイナリの `deja` がローカルで BM25 インデックスを構築するため、LLM や埋め込みモデル、API キーは不要です。明示的に実行する更新・同期コマンドおよび `DEJA_OFFLINE=1` で無効化できる日次リリース確認を除き、通信は発生しません。

## インストール

```sh
pi install npm:@vshulcz/pi-deja
```

システムに `deja` がまだ入っていなくても、[使用されるバイナリ](#使用されるバイナリ)の優先順で同梱コピーへフォールバックします。先に本体を入れておくこともできます：

```sh
brew install deja-vu
```

`deja install pi-auto` や `deja install --auto` で配置済みの拡張機能がある場合は自動的に待機し、重複登録や二重注入を防ぎます。`pi.extensions` と `before_agent_start` を使う仕組みのため `omp` でも同じように動作します。

## 主な機能

- **セッション開始時（フッターステータス）**：プロジェクトで索引済みのセッション数とヒット状況をフッターに表示します。
- **毎回のプロンプト（`before_agent_start`）**：質問内容に関連する過去の決定や手順を検索し、モデルへコンテキストとして渡します。
- **コマンド失敗時（`bash` / `powershell` の `tool_result`）**：コマンドが失敗した直後に、過去同じエラーのあとで成功した対処コマンドを添えます。
- **編集・参照時（read/edit/write の `tool_result`）**：対象ファイルについて過去のセッションで確定していた方針を提示します。
- **コンパクション後（`session_compact`）**：要約後に失われやすい現在のタスク・結論・未完了コマンドを再注入します。
- **セッション終了時（`session_shutdown`）**：終了時の状態に合わせてインデックスを更新します。
- **`/deja <query>` コマンド**：入力欄から全エージェントの履歴を即時検索します。

## 使用されるバイナリ

次の順番で `deja` バイナリを解決します：

1. 環境変数 `DEJA_BIN` のパス
2. `PATH` 上の `deja`
3. 標準的なインストール先（`~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin` など）
4. `@vshulcz/deja-vu` パッケージ同梱バイナリ

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 50 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## プライバシー

インデックスは `~/.cache/deja/index.db`（`DEJA_INDEX_DIR` で変更可）に保存され、構築時に `sk-*`、`ghp_*`、`AKIA*`、`Bearer` トークンを自動マスクします。通常の検索はすべてローカルで完結し、明示的に `deja embed` を設定しない限り外部へ送信されません。50 ツール全体での連携（`recall` / `fix` / `blame` / `handoff`）は `deja install --auto` でまとめて設定できます。

## ライセンス

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。
