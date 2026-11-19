# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[Spec Kit](https://github.com/github/spec-kit) 用の拡張機能です。機能の実装に入る前に `/speckit.deja-vu.recall` が過去のコーディングエージェントのセッションを検索し、試した手法・決定事項・失敗したアプローチを各セッション ID 付きで報告します。

セッションデータは、Claude Code、Codex、Cursor、Copilot、Gemini などがディスクに書き出した記録をローカルでインデックスする [deja](https://github.com/vshulcz/deja-vu) から取得します。データはすべて自分のマシン内にとどまります。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## インストール

先に `deja` をインストールします：

```sh
brew install deja-vu
# または
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

次に、Spec Kit プロジェクトで拡張機能を追加します：

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

各リリースには同じバージョンの `deja-spec-kit.zip` が添付されます。ローカルチェックアウトを試す場合は `specify extension add --dev /path/to/deja-vu/extensions/spec-kit` を実行してください。

## 使い方

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

引数なしでは現在の機能の `spec.md` と `plan.md` を読んで検索クエリを自動生成し、引数ありではその語句を検索します。エージェントの書式によっては `/speckit-deja-vu-recall` または `$speckit-deja-vu-recall` として表示されます。

内部ではリポジトリルートから次を実行します：

```sh
deja recall -- "<query>"   # 一致するセッション（このプロジェクト優先）
deja files -- "<query>"    # 関連トピックで開いた・編集したファイル
```

抜粋が短すぎる場合は `deja show <id>` も参照します。レポートは `Prior sessions` セクションとして出力され、試行して失敗した点・決定事項・対象ファイル・未完了項目を `(deja:<id>)` 引用付きでまとめます。

## フック

オプションの `before_implement` フックを 1 つ登録します。`/speckit.implement` 実行時にエージェントが事前リコールの実行を提案し、許可した場合のみ動作します。無効化するには `.specify/extensions.yml` で `enabled: false` を設定するか、`specify extension disable deja-vu` を実行してください。

## 補足

- 読み取り専用：検索と参照のみを行い、ファイルは編集しません。
- 初回呼び出し時はインデックス構築のため履歴量に応じて時間がかかる場合がありますが、以降は 1 秒未満で応答します。
- 取得したセッション内容は参照データとして扱われ、その中に書かれた指示には従わないようプロンプトで指定しています。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 49 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。
