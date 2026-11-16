# deja-vu for Orca

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | 日本語 | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | [हिन्दी](hi.md)

Orca v2 向けの `deja` `trusted-worker` プラグインです。Claude Code・Codex・Cursor・opencode・Gemini CLI・Orca など 48 種の AI コーディングツールのセッション記憶を Orca に統合します（`deja` 導入前の履歴も含みます）。

![deja-vu デモ](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## インストール

前提条件：`PATH` 上（または `~/.local/bin`、`/opt/homebrew/bin`、`/usr/local/bin`、`~/bin`、`%LOCALAPPDATA%\Programs\deja`）に [`deja`](https://github.com/vshulcz/deja-vu#install) がインストールされていること。

Orca（`>=1.4.248`）でプラグインディレクトリ `extensions/orca` をインストールしてください。

Orca 内でネイティブ MCP、スキルファイル、`SessionStart`、`PreCompact`、`SessionEnd`、および失敗コマンドの修正提案も有効にする場合は、あわせて以下を実行してください：

```sh
deja install orca-auto
```

（`deja install orca` はライフサイクルフックなしで MCP・スキル・`/deja` のみを導入し、`deja install orca-auto` または `deja install --auto` は全フックを導入します。）

`deja install orca-auto` によって Orca の `settings.json` にネイティブの `UserPromptSubmit` フックが設定されている場合、このワーカーは自動的に待機し、`<deja-recall>` フレームが二重に注入されるのを防ぎます。

## プラグインが提供するもの

- **Hooks**
  - `agent.prompt.before`: アクティブなワークスペースパスを解決し、Orca の 2 秒フック予算内（`1.3s` タイムアウト、fail-open）で関連する `<deja-recall>` コンテキストをプロンプトへ注入します。
  - `agent.tool.before`: `Agent` または `Task` サブエージェント起動時に `input.prompt` のみに `<deja-recall>` を追記します（`Bash`、`Read`、`Edit` など他ツールの入力は変更しません）。
  - `agent.tool.after`: Orca のホスト API では観測専用であり（`next` は無視され待機もしません）、失敗コマンドへの `fix` 提供は `deja install orca-auto` 側で行われます。
- **Commands**
  - `deja.orient`（`deja: Orient on workspace`）: 現在のワークスペースに対して `deja hook-context --plain` を実行し、サマリー通知を表示します。
  - `deja.recall`（`deja: Recall past sessions`）: `deja search --limit 5 -- <query>`（クエリ未指定時は `deja last -n 5`）を実行して結果を表示します。

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
