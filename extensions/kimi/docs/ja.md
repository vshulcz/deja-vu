# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub でスター](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu) は Claude Code、Codex、Cursor、opencode など計 49 のエージェントがディスクに書き出したセッションファイルをインデックスし、そこから回答します。このプラグインはそのインデックスを Kimi Code へ組み込み、プロンプト時の自動リコールとエージェントからの履歴検索を可能にします。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## インストール

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

この書式は最新リリースを固定しつつ取得元を記録し、Kimi の更新確認で使われます。リリース資産としてリポジトリ全体を取得せず導入できる約 10 KB の `kimi-deja.zip` も配布しています。

`deja` バイナリがまだない場合は先にインストールしてください（あわせて他のエージェントも接続できます）：

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

プラグインは管理下のコピーから読み込まれるため、導入後は `/reload` または `/new` を実行してください。

## アップデート

Kimi は公式マーケットプレイス経由のプラグインのみ更新通知を出します。リポジトリ直指定で入れた場合は再度 `/plugins install` を実行すると最新版を取得できます。また、インストール済みのコピーが `deja` 本体に同梱の版より古い場合は `deja doctor` が知らせます：

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## 提供される機能

- **`deja install kimi-auto` が設定するフック一式**：セッション初回プロンプトでこのプロジェクトの決定事項を渡し（`deja hook-context --once`）、各プロンプトで入力に合う過去履歴を検索して付加します（`deja hook-prompt`、該当なしなら何も出ません）。コンパクション前には要約対象となるターンを読み取り、次のプロンプトへ作業中の状態を引き継ぎ（`deja hook-precompact`）、`SessionEnd` でライブスタンプを消去します（`deja hook-session-end`）。
- **ツール**：`deja mcp` を MCP サーバとして宣言し、`recall`、`context`、`blame`、`fix`、`how`、`orient`、`remember`、`handoff` の各モードを持つ 1 つの `deja` ツールを提供します。
- **`/deja:recall <query>`**：ユーザー自身が履歴を直接検索したいときのスラッシュコマンドです。
- **`deja-history` スキル**：セッション開始時に読み込まれ、エージェントが再デバッグの前に履歴を確認するようにします。

## どのバイナリが使われるか

優先順は `DEJA_BIN` → 手動インストール先（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin`）→ `PATH` 上の `deja` です。`deja update` や `brew upgrade deja-vu` で更新したバイナリが常に優先されます。

## `deja install kimi` も実行している場合

CLI 版インストーラは `~/.kimi-code/mcp.json` と `config.toml` に同じ MCP サーバとフックを書きますが、Kimi はプラグイン側のサーバを別名前空間（`plugin-deja:deja`）で起動するため、そのままではツールもリコールも二重に動きます。

このプラグインはフック発火時とサーバ起動時に CLI 版の設定有無を都度確認し、存在すれば自身を休止させます（`deja install` 側を優先して最新化できるため）。`deja uninstall kimi` を実行すると再インストールなしでプラグイン側へ制御が戻ります。

プラグインのみで連携されている環境では、`deja doctor` が `kimi` 欄に `plugin` と報告し未設定扱いになりません。

自動検知できない例外は 2 つあります：`deja` 以外の名前で手動登録した MCP エントリ（プラグインと並行動作します）と、インストーラのマーカーコメントなしに自作したフックです。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 49 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ライセンス

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
