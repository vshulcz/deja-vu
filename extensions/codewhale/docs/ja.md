# deja-vu for CodeWhale

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

CodeWhale 向けの Claude 互換プラグインバンドルです。48 種の AI コーディングツールに蓄積されたローカルセッションを横断して、`deja` MCP サーバー（`recall`、`fix`、`blame`、`how`、`orient`、`context`、`remember`、`handoff`）、`deja-history` スキル、`/deja` コマンドを提供します。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## インストール

前提条件：`PATH` 上に [`deja`](https://github.com/vshulcz/deja-vu#install) がインストールされていること。

CodeWhale 内で `/plugin install` を使い、リポジトリのサブディレクトリ（`extensions/codewhale`）からこのバンドルをインストールしてください。

CodeWhale の Claude プラグイン互換ローダーは MCP サーバー・スキル・スラッシュコマンドを読み込む一方、`hooks` と `lspServers` は無視します。`~/.codewhale/config.toml` に CodeWhale ネイティブのライフサイクルフック（`message_submit`、`tool_call_before`、`tool_call_after`、`session_end`）も組み込みたい場合は、`deja install codewhale-auto`（または `deja install --auto`）を実行してください（`deja install codewhale` は MCP と `tools.always_load` のみを設定します）：

```sh
deja install codewhale-auto
```

## バンドル構成

- `.claude-plugin/plugin.json`: プラグインメタデータ（`name: deja-vu`）
- `.mcp.json`: `deja mcp` stdio サーバー設定
- `skills/deja-history/SKILL.md`: `deja` 各モードのスキル指示書
- `commands/deja.md`: `/deja` スラッシュコマンド

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 48 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

[MIT](../LICENSE)

役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
