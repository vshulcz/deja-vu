# Grok Build 用 deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

同じマシン上の Claude Code・Codex・Cursor・opencode・Zed ほか 48 種類のコーディングエージェントのセッション履歴を共有記憶として Grok Build へ接続します。単一 Go バイナリの `deja` がローカルだけで動作し、`DEJA_OFFLINE=1` で日次更新確認も停止できます。

## インストール

プラグインにはバイナリ自体は同梱されないため、まず `deja` を入れます：

```sh
brew install deja-vu
# または：
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

続いて本リポジトリのマーケットプレイスから追加します（公式カタログ申請：[xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)）：

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

または `deja install --auto` を実行すると、`~/.grok/config.toml` の `[mcp_servers.deja]` と `~/.grok/hooks/deja.json` へ同等の構成を書き込みます。

## 含まれる機能

- **`deja` MCP ツール**（8 モード：`recall`・`fix`・`blame`・`how`・`orient`・`context`・`remember`・`handoff`）
- **`deja-history` スキル**および **`/deja:recall <query>`** コマンド
- **5 つのフック（`hooks/recall.mjs`）**：`SessionStart`、`UserPromptSubmit`、`PreToolUse`（`updatedInput`）、`PostToolUse`（Grok 1.0.41 の `Bash` 実行対応）、`PreCompact`、`SessionEnd`

## 併用しても重複しません

`~/.grok/config.toml` に `[mcp_servers.deja]` があるか、`~/.grok/hooks/deja.json` に deja のフックが定義されている場合、プラグイン側は自動的に待機し二重実行を防ぎます。CLI 側の設定を外すには `deja uninstall grok` を使います。

## バイナリの探索順序

`DEJA_BIN` -> `~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`、`/usr/bin` -> `PATH`。

## バイナリ未導入時の動作

`${GROK_PLUGIN_ROOT}` と `.mcp.json` を用いて起動します。Grok Build 1.0.5 以降で `deja` 未インストール時は、`grok mcp doctor` や `grok inspect` が `handshake failed: connection closed` を報告しますが、本体をインストールすると解消します。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1） | **トークン 58% 削減**（同一リポジトリの反復作業） | 19,195 セッションを **13.6 秒**でインデックス / 53 種のエージェント横断で **0.7 ms** 検索 |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ## ライセンス

MIT
