# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[opencode](https://opencode.ai) から同じマシン上の他 51 エージェント（Claude Code・Codex・Cursor・Gemini・Hermes・Zed など）の履歴を参照できるようにするプラグインです。単一 Go バイナリ `deja` によるローカル BM25 検索で動作し、LLM やクラウド API は不要（`DEJA_OFFLINE=1` で更新チェックも無効可）。

## インストール

`opencode.ai/config.json` に追加します：

```json
{ "plugin": ["opencode-deja"] }
```

（opencode 2.x では `"plugins": ["opencode-deja"]`、または `opencode plugin add opencode-deja` を利用してください。）あるいは `deja install --auto` を実行すると `opencode.json` と `plugins/deja.js` が自動構成されます。

## 含まれる機能

### エージェントが直接呼べる 6 つのツール

| ツール | 用途 |
|---|---|
| `deja_recall` | トピック・エラー文・シンボルで過去のセッションを検索 |
| `deja_session` | 一致した前後の会話ターンを読み込む |
| `deja_blame` | 特定ファイルや `path:line` が書かれたセッションを特定 |
| `deja_fix` | 同じエラーを過去に解決したコマンドを返す |
| `deja_how` | このプロジェクトで実際に使ったコマンドやフラグを再現 |
| `deja_remember` | 確定した設計判断をメモとして残す |

### 4 つの自動リコール機能

- **起動時サマリー**：1.x ではトースト通知、2.x ではシステム文脈として `deja-vu — from Codex CLI, 4 months ago: "…"` の形式で注入。
- **20 分間のクールダウンとサブエージェント除外**：同一ディレクトリへの重複挿入を防ぎ、サブエージェントでは自動注入を省きます。
- **失敗コマンドと変更ファイルの補強**：コマンド結果やファイル操作に合わせて過去の解決策・判断を添えます。
- **コンパクション前の保護**：圧縮で落ちる作業コンテキスト（目的・結論・未完コマンド）を引き継ぎます。

## オプション

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

（2.x は `"plugins"` キー）

- `autoRecall`：自動注入の有効化（既定 `true`）
- `tools`：6 ツール公開の有無（既定 `true`）
- `bin`：`deja` バイナリへのパス明示指定

全 52 ツールへ 8 モード（`recall`・`fix`・`blame`・`how`・`orient`・`context`・`remember`・`handoff`）の MCP を一括設定するには `deja install --auto` を実行してください（[エージェント切り替えガイド](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html)）。

## バイナリの優先順位

`bin` 設定 -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`・`/usr/local/bin`・`/opt/homebrew/bin` -> 同梱バイナリ。

## プライバシー

インデックスは `~/.cache/deja/index.db`（`DEJA_INDEX_DIR` 指定可）に置かれ、`sk-*`、`ghp_*`、`AKIA*`、`Bearer` などは構築時に除去されます。`deja embed` を明示利用しない限り外部通信は発生しません。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 52 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ## ライセンス

MIT
