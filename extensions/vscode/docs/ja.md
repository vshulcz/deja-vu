# deja-vu for VS Code, Cursor, Windsurf & Antigravity

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

**解決済みの不具合をコーディングエージェントが何度も調べ直すのを終わらせます。**

[deja-vu](https://github.com/vshulcz/deja-vu) は Claude Code・Cursor・Codex・Windsurf・Copilot Chat・OpenCode・Cline・Roo Code ほか 40 種のコーディングエージェントがディスクへ保存しているセッショントランスクリプトを単一 Go バイナリでローカルインデックスします。LLM 不要・常駐デーモン不要です。

**[★ GitHub でスター](https://github.com/vshulcz/deja-vu)** · **[ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## この拡張機能の機能

IDE ステータスバーに `deja statusline` のライブ状態（各エージェント横断でインデックス済みのセッション数、当日のリコール回数、ワークスペース履歴）を表示します。まだ `deja` が入っていない場合、ステータスバー項目をクリックするとターミナルで `npx -y @vshulcz/deja-vu install --auto` を実行し、検出したすべてのエージェントを自動設定します。

## クイックインストール

```sh
brew install deja-vu && deja install --auto
# Homebrew を使わない場合：
npx -y @vshulcz/deja-vu install --auto
```

CLI からステータスバー拡張をインストールします：

```sh
code --install-extension vshulcz.deja-status
```

## 実測結果

| 計測項目 | 結果 |
|---|---|
| LongMemEval-S 検索精度（クレンジング済み 470 問） | **97.2% R@5**、**88.9% hit@1**（MemPalace 公表値 96.6%、agentmemory 95.2%） |
| 同一リポジトリ作業でのトークン消費量 | **58% 削減** — agentmemory の**半分**（`52,815` 対 コールド時 `103,443`、11 ペア計測） |
| プロセス内語彙検索レイテンシ | 48 種のエージェント横断で中央値 **0.7–0.8 ms** |
| 19,195 件の実セッションに対する初期インデックス時間 | **13.6 s**、API キー不要（7 ツール中の次点は 72 s） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
