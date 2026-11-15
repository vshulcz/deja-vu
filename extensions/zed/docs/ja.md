# deja for Zed

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub でスター](https://github.com/vshulcz/deja-vu)**

[deja](https://github.com/vshulcz/deja-vu) は Claude Code、Codex、Cursor、opencode など計 48 のエージェントがディスクへ残すセッションファイルをインデックス化して回答します。この拡張機能はそのインデックスを Zed のエージェントパネルへ context server として提供し、deja 導入前の履歴も含めて過去の作業を検索できるようにします。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## エージェントに提供される機能

1 つのツール `deja` を `mode` 指定で呼び出します：

| Mode | 回答する内容 |
|---|---|
| `recall` | エラー文字列・関数名・ファイルパス・フラグに一致するセッション。 |
| `context` | 判断の経緯が重要なときの、1 セッション分の完全なダイジェスト。 |
| `blame` | ファイルを編集・削除する前に確認できる、そのファイルを扱ったセッション。 |
| `fix` | 同じエラーの後にこのマシンで実行された修復コマンド。 |
| `how` | この環境で実際に動いたビルド・テスト・デプロイ手順。 |
| `orient` | 過去のセッションがこのプロジェクトで実行したコマンドと触ったファイル。 |
| `remember` | 後で引き出せるよう設計判断を 1 件保存します。 |
| `handoff` | 別セッションの作業状態を取り出し、中断箇所から再開します。 |

## インストール

Zed の拡張機能一覧からインストールします。初回利用時に Zed へ `deja --version` の実行を依頼し、通れば Zed の `PATH` 上の `deja` をそのまま使います。見つからない場合はリリース版の `deja` バイナリを拡張専用ディレクトリにダウンロードします。拡張機能はサンドボックス上で動き通常のインストール先を探索できないため、Zed 起動時の `PATH` に `deja` が含まれない場合は明示指定が必要です：

```json
{
  "context_servers": {
    "deja-context-server": {
      "settings": {
        "binary": "/opt/homebrew/bin/deja"
      }
    }
  }
}
```

`deja install --auto` も Zed に対応しており、`settings.json` へ直接サーバー定義を書き込みます（CLI がある場合はこちらが最短です）。双方とも同じ ID `deja-context-server` を使い、Zed は ID で管理するためどちらを先に導入してもサーバー設定は 1 つだけに保たれます。CLI が書き込んだ設定は `deja uninstall zed` で削除できます。

インデックス作成と検索はローカル完結です。ネットワーク通信は行わず、認証情報もインデックス構築時にマスクされます。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 48 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

MIT ライセンス（deja 本体と同じ）。

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
