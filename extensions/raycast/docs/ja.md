# deja-vu for Raycast

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** の一部（LongMemEval-S で **97.2% R@5** · 19,195 セッションを **13.6 秒**でインデックス · agentmemory の**半分のトークン**）· [ドキュメント](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [ライブデモ](https://vshulcz.github.io/deja-vu/view/) · [ベンチマーク](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub でスター](https://github.com/vshulcz/deja-vu)**

Raycast から Claude Code・Codex・Cursor・OpenCode・Gemini CLI を含む 49 種のコーディングエージェント（他 44 種）のセッションを横断検索して再開できます。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## 前提条件

[`deja-vu`](https://github.com/vshulcz/deja-vu) をインストールします：

```sh
brew install deja-vu && deja install --auto
```

## コマンド

- **Search Sessions**: Mac 上のすべてのコーディングエージェントセッションを全文検索し（`deja search --json --limit 25 -- <query>`）、厳密一致と関連度上位候補を分けて表示します。
- **Recent Sessions**: 導入済みエージェント横断で最新 100 件のセッションを一覧表示し（`deja last 100 --json`）、メタデータプレビュー（`Agent`、`Project`、`Started`、`Updated`、`Session ID`）を確認できます。

## アクション

- **Show Session**: マスキング済みの会話履歴を Raycast 内で閲覧します（`deja show <id> --json --limit 200`）。
- **Resume in Terminal**: `Terminal.app` でそのエージェント本来の再開コマンドを実行します（`deja resume <id>`）。
- **Copy Resume Command**、**Copy Session ID**、**Open Project Folder**。

## ベンチマーク

| 再現率 | トークン消費 | インデックス & 検索速度 |
|---|---|---|
| LongMemEval-S で **97.2% R@5**（470 問で 88.9% hit@1；MemPalace 公表値 96.6%、agentmemory 95.2%） | **トークン 58% 削減**（反復作業において agentmemory の**半分のトークン**） | 19,195 セッションを **13.6 秒**でインデックス / 49 種のエージェント横断で **0.7 ms** 検索（7 ツール中の次点 72 秒） |

## リポジトリメモリとエージェント間の引き継ぎ

`.deja/memory.jsonl` を git で共有し、チームで採用した設計判断や回避すべき失敗パターンを共有できます（`deja repo candidates`、`deja repo add <id>`）。記録時に自動マスキングされ、コンテントハッシュ順・`merge=union` 属性付きで保存されるため並列ブランチでもコンフリクトせずマージされ、クローンした全員のセッションで読み込まれます（信頼ポリシーの `{"activations":{"auto":{"repo":false}}}` でフック注入を無効化可能）。[詳細](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)。

作業の途中でツールを切り替えるときは、`deja handoff --to <agent>`（または MCP 経由で `handoff`）を実行するだけで、目標・決定事項・変更ファイル・通過したテスト・残作業を別エージェントへ引き継げます。

## ローカル開発

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu) の一部です。MIT。役に立ちましたか？ [GitHub で deja-vu にスターを付ける](https://github.com/vshulcz/deja-vu)。
