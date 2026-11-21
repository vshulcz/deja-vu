# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

IntelliJ IDEA、GoLand、PyCharm、WebStorm、RustRover、CLion、PhpStorm、Rider（`2024.2+`）向けのローカル横断セッション検索、AI 行単位 blame、エージェント間引き継ぎプラグインです。

[deja-vu](https://github.com/vshulcz/deja-vu) は、Claude Code、Codex、OpenCode、Cursor、Gemini CLI、Junie、GitHub Copilot、Aider、Windsurf など計 52 のコーディングエージェントがディスクに書き出すセッション記録を BM25 でローカルインデックス化し、シークレットをマスクしたうえでクラウド不要で参照します。

**[ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 主な機能

- **ステータスバーウィジェット**：現在開いているプロジェクトの `deja statusline` 統計を IDE ステータスバー右側にライブ表示します。クリックすると `deja-vu` アクションポップアップが開きます。
- **AI セッション検索（`Tools → deja-vu → Search AI Sessions...`）**：対応する全 52 エージェントのローカルセッションを横断検索（`deja search --json`）し、選択した履歴を読み取り専用 Markdown タブで開きます。
- **最近の AI セッション（`Tools → deja-vu → Recent AI Sessions`）**：マシン上の直近 50 セッションを一覧表示（`deja last 50 --json`）し、インクリメンタル検索で絞り込めます。
- **現在行の AI Blame（エディタ右クリックメニュー / `Tools → deja-vu → AI Blame Current Line`）**：`deja blame <file>:<line> --json`（該当行がなければ `<file>` にフォールバック）を実行し、カーソル行を書いた・変更した AI セッションを開きます。
- **エージェント間ハンドオフのコピー（`Tools → deja-vu → Copy Cross-Agent Handoff`）**：プロジェクトルートで `deja handoff` を実行し、ダイジェストをクリップボードにコピーして読み取り専用 Markdown タブで表示します。

## インストール

1. `deja` CLI をインストールしてローカルのコーディングエージェントと連携します：

```sh
brew install deja-vu && deja install --auto
# Homebrew を使わない場合:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. JetBrains プラグイン（[JetBrains Marketplace の `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)）をインストールします：

- **JetBrains Marketplace から**：[plugins.jetbrains.com から直接インストール](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)するか、**Settings → Plugins → Marketplace** で `deja-vu` を検索します。
- **ディスクから**：[GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) から `deja-jetbrains.zip` を取得し、**Settings → Plugins → ⚙️ → Install Plugin from Disk...** で選択します。
- **CLI から**：

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(お使いの IDE に合わせて `idea` を `goland`、`pycharm`、`webstorm`、`rustrover`、`clion` に読み替えてください。)*

## ソースからのビルド

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

インストール可能なアーカイブは `build/distributions/` に出力されます。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 52 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)
