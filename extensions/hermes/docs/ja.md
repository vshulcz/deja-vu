# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub でスター](https://github.com/vshulcz/deja-vu)**

このマシンにすでにあるコーディングセッション（Claude Code、Codex、Cursor、Hermes 自身、その他 deja が読み取れる全エージェント、ツール導入前の数か月分も含む）から回答する Hermes 用メモリプロバイダです。外部モデルも API キーも常駐サーバも不要で、単一の Go バイナリ [deja](https://github.com/vshulcz/deja-vu) がローカルでインデックスを構築します。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## インストール

あらかじめ `deja` が PATH にある状態にします：

```bash
brew install deja-vu        # または: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # Hermes とこのマシン上の対応エージェント一式を連携
hermes plugins install deja-memory   # または GitHub から: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

あるいは `hermes memory setup` で `deja-memory` を選ぶと、バイナリの存在確認を行ってから有効化します。

## 動作概要

- **最初のターン前**：Claude Code や Codex 向けフックと同じダイジェスト（このプロジェクトの直近の作業と確定事項）を Hermes に渡します。以降は現在の入力が過去に一致するときのみ付加され、ほとんどのターンでは何も足しません。
- **取得内容の可視化**：deja が Claude Code で表示する行（どのエージェント由来の記憶かを示すレシート、開始時リコール、ビルド／週次ノート）は、対話 CLI と `hermes chat -q` の Hermes リコール行 `🧠 deja · …` に載ります。モデルの出力テキスト自体は変わりません。Hermes 0.17 にはリコール行がなく、`hermes -z` や `hermes chat -Q` では出力されず、TUI ではステータスバーに一瞬表示されるだけ、またゲートウェイチャットは複数人に見られる可能性もあるため、それらでは表示可能なセッション向けにノートを保持します。
- **cron 実行時は自動リコールなし**：Hermes のプラットフォームが `cron` の場合、プロバイダは自動で何も追加しません。ジョブ側から deja ツールを呼べば通常どおり応答します。
- **3 つのツール**：`deja_recall`（過去セッション検索）、`deja_fix`（前回同じエラー後に実行した対処）、`deja_blame`（ファイルを編集したセッション一覧）。
- **ノートの優先度維持**：Hermes が `MEMORY.md` や `USER.md` に追加した項目も deja ノートとして保持され、周囲のセッションより上位にランクされます。
- **コンパクション対応**：コンパクション直前に作業中だった内容を次ターン用に保持し、セッション終了時にはライブマークを外します。

メモリプロバイダ単体ではできないことが 1 つあります：直前に失敗したコマンドの隣へ過去の修正手順を添える処理です。これはプラグインフック `transform_tool_result` を通りますが、Hermes がメモリプロバイダに渡すコンテキストでは `register_hook` が何もしないためです（`plugins/memory/__init__.py`、0.17.0）。`deja install hermes-auto` を実行すると、それを担うフックプラグインも一緒に書き込まれます。

Hermes の各ターンを別途書き出すこともありません。Hermes 自身が保存しているストアを次回リフレッシュ時に直接読み取ります。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 48 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## `deja install` と同じソースコード

`deja install hermes-auto` は `~/.hermes/plugins/deja-memory` にこのプロバイダを書き込み、同時に MCP サーバと `pre_llm_call` フックプラグインを配置します。このディレクトリのファイルは `cmd/deja/install_hermes_memory.go` から同一ソースとして生成されており、違いはバイナリパスを埋め込むか PATH から探すかだけです。両者がずれると `TestHermesExtensionIsTheProviderInstallWrites` が失敗します。

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
