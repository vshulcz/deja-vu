<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>すべてのコーディングエージェントが共有するひとつの記憶。材料は、すでにディスク上にある履歴です。</b></p>

<p align="center">あなたのエージェントは、3月に直したはずの問題を、また一からデバッグしようとしています——しかも前回とは別のエージェントで。
deja は、Claude Code、Codex、Cursor をはじめ、このマシン上のあらゆるエージェントがすでにディスクに書き出したセッションをインデックス化し、
どのエージェントから尋ねられても、該当するセッションを返します。</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="同じエージェントに同じ質問を2回する：記憶がないと何の記録もないが、deja があると8か月前の決定をもとに答える"></p>

<p align="center"><sub><em>誰も検索していません——エージェントが自分で deja を呼び出しました。実際のモデルと実際のツール呼び出しによる2回の本物の実行で、対象は合成コーパスです。誰の履歴も公開していません。</em></sub></p>

<p align="center"><b>deja は最初から満杯の状態で始まります。34 のエージェントがすでに書き残した履歴を、インデックス作成中から検索でき、モデルもキャプチャ手順も不要です。</b></p>

<p align="center">しかも、誰かが頼む必要もありません。リコールはセッション開始時、プロンプトごと、
ファイルが編集される前やコマンドが実行される前、そしてコマンドが失敗した後に届きます。キーやトークンはインデックス作成時に取り除かれます。
何が取り除かれ、何は取り除けないのかは[セキュリティモデル](../../docs/SECURITY-MODEL.md)に書いてあります。</p>

<p align="center">
このマシンが一度解いた作業では <b>token が 58% 少ない</b> &middot; LongMemEval-S（470 問のクリーン版）で <b>88.1% hit@1</b> &middot; LoCoMo で <b>70.5% retrieval hit@1</b> &middot; 数 GB の履歴に対して<b>ミリ秒</b>単位の検索<br>
<sub>各アーム 11 回の実行で 53,558 token、記憶をつながない場合の 126,222 に対して。同じ台の後日の実行では 71% 減 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">1 つの作業を終えるまでの費用</a> &middot;
検索側のハーネスはどちらもこのリポジトリに含まれており、公開データセット上で数分で実行できます &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">数字をご自身で確かめてください</a></sub>
</p>

<p align="center">
  <a href="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml"><img src="https://github.com/vshulcz/deja-vu/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vshulcz/deja-vu/releases"><img src="https://img.shields.io/github/v/release/vshulcz/deja-vu" alt="Release"></a>
  <a href="https://mcptoplist.com/server/io.github.vshulcz%2Fdeja-vu"><img src="https://mcptoplist.com/badge/io.github.vshulcz%2Fdeja-vu.svg" alt="MCP Toplist"></a>
  <a href="../../LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | 日本語 | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">ドキュメント</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">ベンチマーク</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">他ツールとの比較</a> &middot; <a href="../../docs/INTEGRATING.md">自分のツールへの組み込み</a></p>
<p align="center"><sub>役に立ったら、<a href="https://github.com/vshulcz/deja-vu">GitHub で deja-vu にスター</a>をお願いします。</sub></p>

## インストール

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/banner.png" width="700" alt="最初のインデックス作成後に deja が表示するもの：ロゴ、見つかったエージェント、そしてあなた自身の履歴から取ったクエリ"></p>

インストールに 10 秒、インデックス作成に約 10 秒で、すぐに使えます。2 つ目のコマンドは、見つかったすべての
エージェントに MCP リコールを接続し、対応しているエージェントではセッション開始時のリコールを有効にし、
最初のインデックスを作成するので、次のセッションでその待ち時間は発生しません。

新しいエージェントセッションを開始して、何か月も前に取り組んだことを聞いてみてください：

> have we dealt with jwt refresh rotation before? check your memory

頼む必要すらありません——自動リコールを有効にしていれば、セッションを開いた時点で、
エージェントはそのプロジェクトで解決済みのことをすでに知っています。

<details>
<summary>その他のインストール方法と、すべては不要な場合の設定</summary>

`brew install deja-vu`、`go install github.com/vshulcz/deja-vu/cmd/deja@latest`、
または何もインストールせずに試すなら `npx @vshulcz/deja-vu "query"`。MCP サーバーをバンドルとして
受け付けるデスクトップアプリでは、[最新リリース](https://github.com/vshulcz/deja-vu/releases/latest)の
`.mcpb` を開けます。バイナリも同梱されています。

Claude Code、Codex、Cursor、Qwen、OpenClaw、Copilot では、それぞれのマーケットプレイスから
同じプラグインバンドルを入れることもできます：

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

Windows ではインストールスクリプトが `unsupported OS` で終了します——シェルスクリプトだからです。
代わりに Scoop を使ってください。Scoop に標準で入っている main バケットから入手できます：

```powershell
scoop install deja-vu
```

または[最新リリース](https://github.com/vshulcz/deja-vu/releases/latest)から
`deja-vu_<version>_windows_amd64.zip` を取得し、`deja.exe` を `PATH` の通った場所
（例：`%USERPROFILE%\.local\bin`）に置いてください。

検索だけならバイナリ単体で完全なインストールです。インデックス作成、検索、`show`、`ctx`、`blame`、
`--json`、秘匿化（リダクション）には他に何も要りません。`deja install` は MCP をエージェントに接続し、
セッション開始時のリコールを有効にするためのもので、あると便利ですが任意です。バイナリのみの構成では
`deja doctor` がすべての MCP ターゲットを `not-wired` と報告しますが、これはその構成として想定どおりの
動作です。`deja warmup` は `~/.agents/skills/deja-search/SKILL.md` にスキルも配置します。このスキルは
エージェントに CLI の使い方——`deja search --json`、`ctx`、`blame`、`tier` と `total` の読み方——を教えるので、
MCP なしでも履歴を検索できることをエージェントが理解できます。リポジトリ内のコピーは
[`skills/deja-search/SKILL.md`](../../skills/deja-search/SKILL.md) です。

`deja install --all` は、セッション開始時のリコールを除いた `--auto` です。エージェントは各セッションを
記憶とともに始めるのではなく、自分で呼び出すと判断したときに記憶から答えます。
[エージェント設定ガイド](https://vshulcz.github.io/deja-vu/guide/agents.html)では、各ハーネスの対応状況、
aider の読み取り専用コンテキストファイル、Windows の `cmd /c deja mcp` ラッパーについて説明しています。

</details>

<details>
<summary>各エージェント自身のガイダンスファイルに書き込まれる内容</summary>

インストール時には、検出したハーネス向けにユーザーレベルのガイダンスも書き込みます。Claude Code、Codex、opencode、Gemini CLI、Antigravity、Qwen、Kimi Code、pi、Senpi、Copilot、VS Code Copilot Chat、Cursor、Goose、OpenClaw、Hermes、Roo Code、omp、Amp、prime-agent、DeepSeek Harness、Continue、Crush、Zed は、それぞれ自身のガイダンスファイル（または設定された `XDG_CONFIG_HOME` 配下）に書き込まれます。再実行すると、周囲のユーザー記述はそのままに、deja のスキルまたはマーク付きブロックだけを書き換えます。オプトアウトするには `deja install --all --no-guidance` を使ってください。Grok Build には、それが読み込む `~/.agents/skills` に共有スキルが置かれます。その横に書かれる `~/.grok/GROK.md` は、同じディレクトリを使う無関係なコミュニティ製 CLI 向けです。Cursor にはユーザーレベルの指示ファイルがないため、Cursor がスキルを読み込む 4 か所のひとつである `~/.agents/skills` に共有スキルが置かれます——毎セッションではなく、関連がありそうなときにだけ読み込まれます。

</details>

## できること

**Codex で解決して、Claude が覚えている。** 34 のコーディングエージェントはすべての会話を
ローカルファイルに書き出しています。deja はそれらのファイルを、全エージェントが読めるひとつの記憶レイヤーに変えます。

| | |
| --- | --- |
| **さかのぼって検索** | `deja "connection pool exhausted"` で数 GB を検索。deja をインストールする前の履歴もすべて対象です。自然言語の質問は関連度ティアにフォールバックします。時間はフィルターではなくヒントとして扱われます。 |
| **エージェント横断のリコール** | MCP の `deja` ツールを `recall` モードで使えば、元々どのエージェントで解決したかに関係なく、尋ねたエージェントで *「これは3週間前に直した」* に答えます。 |
| **コンパクションを生き延びる** | 43 回のコンパクションで計測したところ、要約に残ったのは決定事項の 77%、実行したコマンドの 0.2% でした。deja は残りの 99.8% を返します——さらに Claude Code と Codex では、コンパクション開始時にタスク、ファイル、コマンドを捕捉し、次のセッションで一度だけ返します。 |
| **行動の直前にリコール** | エージェントがファイルを編集したりコマンドを実行したりする前に、deja はそのファイルに関する過去の決定、そのコマンドの動作実績のある呼び出し方、あるいはこのマシンにないプログラムを示します。コマンドが失敗すると、`PostToolUse` フックが、このマシンで以前同じエラーの後に何が行われたかを答えます——エージェントが自分では尋ねようとしない組み合わせです。 |
| **会話だけでなく作業もインデックス化** | 各ターンで開いたファイル、実行されたコマンドとその終了ステータス、編集で置き換えられた正確な範囲。どの要約も捨ててしまう部分です。 |

<details>
<summary>さらに 4 つ：却下された決定、情報の鮮度、同期と引き継ぎ、秘匿化</summary>

| | |
| --- | --- |
| **何が定着したかを知っている** | `deja promote <id> --state rejected --note "why"` で、取り消した決定に印を付けます。以降そのセッションがヒットするたびに、試して却下されたことが理由とともに表示されます。何も削除されず、`--state accepted` で印を取り消せます。 |
| **前提が変わったら教えてくれる** | ヒットには *このセッションで触れた 4 ファイルがその後変更されています* と表示され、判断できない場合は何も言いません。「変更なし」と断言することは決してありません。 |
| **同期と引き継ぎ** | `deja sync ssh laptop` で記憶をマシン間で移動します。追記のみで、間にクラウドを挟みません。`deja handoff --to codex` は現在のコンテキストをまとめ、別のエージェントで作業を続けられるようにします。 |
| **秘匿化（リダクション）** | キー、トークン、JWT、秘密鍵ブロックはインデックス作成時に取り除かれます。パターン一致なので、規則が知らない形のものは通り抜けます。境界はセキュリティモデルに記載しています。 |

</details>

### あなた自身の作業の振り返り

`deja stats --card` はターミナルに描画します。ファイル名を渡すと、プロフィール README 用の SVG を
書き出します。他の場所に投稿するなら、[PNG に変換](https://vshulcz.github.io/deja-vu/card/)してください——
そのページはあなたのブラウザ内で変換します。

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja stats カード：1年分のエージェントセッションのヒートマップ、その出どころのエージェント、最長のセッション"></p>

機能の完全なリファレンスは[ドキュメント](https://vshulcz.github.io/deja-vu/)にあります。

## プライバシー

インデックス作成と検索はローカルで完結します。ネットワークを使うのは `deja update`、`deja sync ssh`、
`deja doctor` のバージョンチェック、そして自分で設定したエンドポイントに対する `deja embed` だけです。

認証情報はインデックス作成時に秘匿化されます：AWS キー、`api_key=` や `token=` の代入、
Bearer トークンや生の JWT、PEM 秘密鍵ブロック、各プロバイダーのトークン、`scheme://user:pass@host`
形式の URL、既知のパターンに当てはまらない形の高エントロピー値、そして文章中で述べられたパスワード——
「the admin password is …」のように、他のルールが手がかりにする区切り文字がないもの。
値は `[redacted:<kind>]` に置き換えられ、周囲のテキストは検索可能なまま残ります。`deja share` と
`deja sync export` は、出力時にもう一度秘匿化を適用します。

元のトランスクリプトは秘匿化されません。エージェントはコマンドの出力をそのまま書き込むため、
`cat .env` や貼り付けた接続文字列は平文のまま残ります。`deja secrets` は、秘匿化が残したマーカーをもとに、
どのセッションにどの種類の認証情報が含まれているかを一覧表示します。値そのものは決して表示しません。
あるマシンでは 42 セッションに 84 件ありました（[トランスクリプト内の認証情報](https://vshulcz.github.io/deja-vu/guide/credentials-in-transcripts.html)）。
`deja secrets --scrub` は、まだ値を持つトランスクリプトを書き換えます。値のあった場所に同じ
`[redacted:<kind>]` マーカーを置き、元のファイルは隣に残します。レポートが名前を挙げた種類だけを
対象とし、エージェントが入っているセッションは書き換えず、届かなかった件数を表示します。
セッションごとのファイルを持たないストアは、そもそも書き換えられません。

`deja forget` は再構築したインデックスからセッションを削除し、トゥームストーンを書き込むので、後で
`deja index` を実行しても元の履歴から復元されることはありません。`--unforget` でトゥームストーンを解除します。
プロジェクトの除外は `~/.config/deja/exclude` に 1 行 1 パターンで記述します。`harness:` で始まる行は
ストアを指定します——`harness:opencode` のように——deja はそのストアを走査せず、読み取りに必要なツールも要求しません。

[セキュリティモデル](../../docs/SECURITY-MODEL.md)に、データフロー、秘匿化の限界、信頼の前提、
リリースの検証方法をまとめています。

## CLI

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

**履歴に尋ねる**

| コマンド | 説明 |
| --- | --- |
| `deja <query>` | すべての履歴を検索します。複数語は AND、引用符で囲んだフレーズは連続したテキストを要求します。完全一致がない場合は語形変化や近い綴りを試すので、部分文字列から単語にたどり着けます（`code` で `opencode` が見つかります）。 |
| `deja` | インデックスがありターミナルから実行した場合：今日のセッション、提供したリコール、複数のセッションで尋ねた質問、そしてエージェントが何度もぶつかっている壁を表示します。 |
| `deja wip` | このディレクトリで直前のセッションが何をしていたか：タスク、決着したこと、作業中のファイル、最後のコマンドとそれが失敗したかどうか——誰かが書き忘れなかったメモではなく、トランスクリプトから導き出します。 |
| `deja blame <path>[:line]` | どのセッションがそのファイルについて議論し、何を決め、なぜそうしたか。行を指定すると：その行を最後に変更したコミットと、その行を書いた、あるいはコミットが置き換えたテキストを書いたセッション。`--attribution` は行についての答えだけを出力し、`--json` と併用すると JSON で、`--git-note` と併用すると `refs/notes/deja` に記録します。 |
| `deja files <topic>` | 逆方向：あるテーマに関する作業が実際に触れたファイル。 |
| `deja how <tool>` | このマシンで実際にどう実行されているか。以前エージェントが実行した内容から、実際のフラグ付きで示します。 |
| `deja fix <error>` | 以前同じエラーが起きたとき、このマシンで何を実行し、エラーが再発しなかったか。 |
| `deja friction` | 3 つ以上の別々のセッションで発生したエラーを、ハーネス名付きで表示します。 |

<details>
<summary>見つけたものを活用し、マシン間で移動する</summary>

**見つけたものを使う**

| コマンド | 説明 |
| --- | --- |
| `deja ctx <query>` | 最も一致したセッションの Markdown ダイジェスト。そのままプロンプトにパイプできます。 |
| `deja resume <id>` | 見つかったセッションを、元のハーネスで再開します。 |
| `deja restore <path>` | エージェントが置き換えた範囲を、その編集が記録した `old_string` から取り戻します。元のファイルを上書きすることはありません。 |
| `deja promote <id>` | セッションを、出典・タグ・ライフサイクル状態を持つ厳選ノートに凝縮します。ノートは生のトランスクリプトより上位にランク付けされます。 |
| `deja share <id>` | 同僚向けにサニタイズしたセッションダイジェスト。シークレットは除去済みです。 |

**移動と確認**

| コマンド | 説明 |
| --- | --- |
| `deja sync export/import/ssh` | 記憶をマシン間で移動します。ウォーターマーク付き、追記のみ、冪等です。 |
| `deja view` | 記憶全体をひとつのローカル HTML ファイルとして出力します。サーバーは不要で、ファイルがマシンの外に出ることはありません。 |
| `deja stats` | エージェント作業の振り返り。`--card` でターミナルに描画、`--card <file>.svg` でプロフィール用 SVG を書き出し、`--html` で閲覧可能なタイムラインを生成します。 |
| `deja secrets [--scrub]` | どのセッションの元トランスクリプトに、どの種類の認証情報が含まれているか。値は決して表示しません。`--scrub` は届く範囲を書き換え、元のファイルは隣に残します。 |
| `deja doctor [--deep]` | 自己診断。`--deep` を付けると、インデックスがソースと一致していることを検証します。 |
| `deja mcp` | stdio MCP サーバー。`deja install` が接続するのはこれです。 |

</details>

完全なリファレンス：[コマンド](https://vshulcz.github.io/deja-vu/guide/commands.html)と
[JSON 出力](../../docs/json-output.md)。

### MCP ツール

サーバーは `mode` を持つ `deja` というツールをひとつ公開しています。`deja install` が接続するので、
これが必要になるのはエージェントを手動で設定する場合だけです。以前の 6 つのツール名
（`recall`、`recall_context`、`blame`、`fix`、`how`、`remember`）も、すでにそれらに接続されているものに対しては引き続き応答します。

<details>
<summary>引数と戻り値の形式</summary>

| ツール | 引数 | 戻り値 |
| --- | --- | --- |
| `deja` | `mode`、および `query`、`path`、`error`、`what`、`text`、`tags?`、`harness?`、`project?`、`since?`、`limit?`、`offset?`、`all?` | モードによって異なります（下表）。 |

| モード | 読み取る引数 | 戻り値 |
| --- | --- | --- |
| `recall` | `query`、`harness?`、`limit?`、`offset?` | 密度の高い一致スニペット（上限 4KB）。 |
| `context` | `query`、`harness?` | 最も一致したセッションの Markdown ダイジェスト。 |
| `blame` | `path`、`harness?`、`project?`、`since?`、`limit?`、`all?` | そのファイルについて議論したセッション。 |
| `fix` | `error`、`project?`、`limit?` | 以前同じエラーの後に、このマシンで実行または変更された内容。 |
| `how` | `what`、`project?`、`limit?` | ここでエージェントが実行した内容に基づく、実際の呼び出し方。 |
| `remember` | `text`、`project?`、`tags?` | 後でリコールするために、恒久的な決定を保存します。 |

</details>

## 対応ハーネス

自動リコールをインストールすると、Claude Code と Codex はコンパクション開始時に deja へ
トランスクリプトを渡し、deja は要約が捨てようとしているもの——タスク、結論、ファイル、
各コマンドとその結果、未解決のこと——を保持します。同じセッションとワークスペースに対する次のフックが、
4 KB の予算内でそれを一度だけ返し、その後リポジトリが変化したかどうかを示す一行を添えます。`deja stats`
は、コンパクション後に最初の編集が行われるまでのツール呼び出し回数を数えます。これがこの機能の効果を
測る指標です。何を読み、何を保存し、どこに限界があるかは
[自動コンパクション復旧](../../docs/compaction.md)を参照してください。

<!-- matrix:start -->
aider &middot; Amp &middot; Antigravity &middot; Claude Code &middot; Cline &middot; Codex CLI &middot; Copilot CLI &middot; VS Code Copilot Chat &middot; Cursor &middot; DeepSeek Harness &middot; Gemini CLI &middot; Goose &middot; Grok Build &middot; Hermes &middot; Kimi Code &middot; omp (Oh My Pi) &middot; OpenClaw &middot; opencode &middot; Continue &middot; Crush &middot; pi &middot; prime-agent (PrimeIntellect) &middot; Qwen Code &middot; Cherry Studio &middot; Senpi &middot; gajae-code &middot; Kimchi Coding &middot; Command Code &middot; ZCode &middot; Kiro &middot; Kilo Code &middot; Roo Code &middot; Zed &middot; CodeWhale.

<details>
<summary>各ハーネスの対応状況</summary>

| ハーネス | MCP リコール | 自動リコール | スキル | コマンド | 再開 | 引き継ぎ | 必要なもの |
| --- | :-: | :-: | :-: | :-: | :-: | :-: | --- |
| aider | ⚠ | ✅ | ✕ | ⚠ | ✕ | ✅ | deja aider |
| Amp | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Antigravity | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Claude Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Cline | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Codex CLI | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Copilot CLI | ✅ | ✕ | ✅ | ✅ | ✅ | ✅ | — |
| VS Code Copilot Chat | ✅ | ✕ | ✅ | ✅ | ✕ | 貼り付け | — |
| Cursor | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3（IDE チャット） |
| DeepSeek Harness | ✅ | ✅ | ✅ | ✅ | ✕ | 貼り付け | zstd |
| Gemini CLI | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Goose | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | deja goose |
| Grok Build | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3（grok-dev ストア） |
| Hermes | ✅ | ✅ | ✅ | ✅ | ✅ | 貼り付け | sqlite3 |
| Kimi Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| omp (Oh My Pi) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| OpenClaw | ✅ | ✅ | ✅ | ✅ | ✅ | 貼り付け | — |
| opencode | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3 |
| Continue | ✅ | ⚠ | ✅ | ✅ | ✅ | 貼り付け | — |
| Crush | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | sqlite3 |
| pi | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| prime-agent (PrimeIntellect) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Qwen Code | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Cherry Studio | ✅ | ✕ | ✅ | ✕ | ✕ | 貼り付け | 設定 -> MCP でサーバーを一度インポートし、エージェントでスキルを有効化 |
| Senpi | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | なし |
| gajae-code | ✅ | ✅ | ✅ | ✅ | ✅ | 貼り付け | なし |
| Kimchi Coding | ✅ | ⚠ | ⚠ | ⚠ | ✅ | 貼り付け | なし |
| Command Code | ✅ | ✅ | ✅ | ✅ | ? | 貼り付け | なし |
| ZCode | ✅ | ✅ | ? | ? | ? | 貼り付け | CLI データベース用の sqlite3 |
| Kiro | ✅ | — | ✕ | ? | ✅ | 貼り付け | なし |
| Kilo Code | ✅ | ⚠ | ✅ | ✅ | ✅ | 貼り付け | CLI ストア用の sqlite3 |
| Roo Code | ✅ | ⚠ | ✅ | ✅ | ✅ | 貼り付け | roo CLI（エディターのタスクはエディターで再開） |
| Zed | ✅ | ✕ | ✅ | ✅ | ✕ | 貼り付け | sqlite3 + zstd |
| CodeWhale | — | — | ? | ? | ✅ | 貼り付け | なし |

✅ 動作する &middot; — 可能だが未実装 &middot; ✕ ハーネス側にその仕組みがない &middot; ⚠ 上流のバグにより利用不可 &middot; ? 未調査

</details>
<!-- matrix:end -->

ストアの場所をカスタマイズするには `DEJA_*_ROOT` 変数を使います。各エージェント自身の移動用変数も
尊重されます。[セッション形式レジストリ](https://vshulcz.github.io/deja-vu/registry/README.html)には、
ハーネスごとに観測されたパス、レコードスキーマ、ロールの対応付けがまとめられており、合成フィクスチャによって
それらの記述がパーサーと一致していることを確認しています。

### 独自パッケージを持つハーネス

`deja install --auto` は、他のハーネスと同様にこの 6 つもすべて接続し、それが最短の方法であることに
変わりはありません。これらには各エコシステム内のパッケージもあり、CLI ではなくそこから拡張機能を
インストールする人向けです：

| ハーネス | パッケージ | インストール |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | プラグイン `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | プラグイン `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu` の後に `codex plugin add deja-vu@deja-vu` |
| Grok Build | プラグイン `deja` | `grok plugin marketplace add xai-org/plugin-marketplace` の後に `grok plugin install deja` |

どちらの方法も単独で十分で、両方を使っても問題ありません。opencode、dsh、Kimi、Grok、Codex の
パッケージは `deja install` が書き込んだ内容を読み取り、足りないものだけを追加します。Zed では両方が
同じサーバー ID を使うので、どの順番でインストールしても重複は生じません。

いずれもすでに入っている deja を使い、同梱のコピーはフォールバックにすぎません。

同じ検索はスキルとしても提供されており、`SKILL.md` を読み込むあらゆるエージェントで使えます：

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

このスキルは上記のインストール手順で入れた `deja` バイナリを動かすもので、バイナリは同梱していません。

## セマンティックリコール（任意）

`DEJA_EMBED_URL` で `deja embed` をローカルの Ollama、LM Studio、または OpenAI 互換エンドポイントに
向ければ、言い換えたクエリでもヒットするようになります。到達可能なランタイムがなくても、字句検索と
MCP リコールはそのまま動作します。OpenAI Platform は標準の API キーで使えます：

```sh
export OPENAI_API_KEY='sk-...'
export DEJA_EMBED_URL='https://api.openai.com/v1/embeddings'
export DEJA_EMBED_MODEL='text-embedding-3-small'
deja embed
```

`DEJA_EMBED_URL` が未設定の場合、deja は `localhost:11434` と `localhost:1234` を探索するので、
すでに Ollama や LM Studio が動いているマシンでは何もしなくても検出されます。
`DEJA_EMBED_OFF=1` または `DEJA_EMBED_URL=off` でこの探索を無効にできます——それ以外の値を
`DEJA_EMBED_URL` に設定した場合は、その設定が優先されます。

認証が必要な他の OpenAI 互換エンドポイントでは、`DEJA_EMBED_KEY` を明示的に設定してください：

```sh
export DEJA_EMBED_URL='https://example.com/v1/embeddings'
export DEJA_EMBED_MODEL='embedding-model'
export DEJA_EMBED_KEY='...'
deja embed
```

`DEJA_EMBED_KEY` が優先されます。`OPENAI_API_KEY` が自動的に使われるのは HTTPS の
`api.openai.com` の URL に対してだけで、ローカルやサードパーティのエンドポイントに暗黙的に送られることはありません。

<details>
<summary>ベクトルの保存場所とコスト</summary>

サイドカーは `index.db` の中ではなく、インデックスの横に `.vectors.bin` として置かれます。1,024 次元の
モデルでは、Float32 ベクトルのサイズはメッセージ 1k 件あたり約 4 MB です。リモートエンドポイントが
受け取るのは、秘匿化済みでインデックス化されたテキスト（約 2k 文字に切り詰め）だけで、生のソースファイルは
決して送られません。Ollama や LM Studio を使えば、埋め込みはローカルで完結し、キーも不要です。

</details>

## 検証

```sh
deja bench recall     # ranking floor: 100 queries, half Russian, CI fails if recall drops
deja bench context    # 30 seeded task chains plus five negative controls
deja bench block      # does the answer survive into what deja hands over
deja bench prompt     # what the per-prompt hook fires on, and what it fires on wrongly
deja bench ingest     # what an update costs: unchanged, a turn, a new transcript, a rename, a rewrite
deja bench read       # what it costs to read a database-backed store, and what one long value does to it
```

`bench block` は他の 3 つでは問えない問いを立てます：正しいセッションが手元にあるとき、そのブロックは
そのセッションで決着した内容を運んでいるか。各テーマについて 8 つのセッションが議論し、そのうち 1 つが
決着をつけます。それもトランスクリプトの最後ではなく途中で——そのため、上位ヒットの最新ターンを使う
ベースラインは 0 点になり、0 点を超えるには選び取る必要があります。

| アーム | 答えを含む | トークン数の中央値 |
|---|---|---|
| `deja-block`（セッション開始ブロック） | 1.00 | 665 |
| `deja-digest`（コンテキストダイジェスト） | 1.00 | 1656 |
| `newest-turn`（ベースライン） | 0.00 | 289 |
| `cold` | 0.00 | 0 |


コンテキスト実験では、deja-recall を全履歴、単純な grep、コールドコンテキストと比較します。
デフォルトのシードでの結果：

| アーム | トークン数の中央値 | カバレッジの中央値 | ネガティブコントロールのトークン数 |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

生ログを grep するのと同じ事実カバレッジを約 250 分の 1 のトークンで、一致したセッションを丸ごと
再生するより約 70 分の 1 のトークンで実現し、関連する過去の事実がないチェーンでは何も注入しません。
コーパス生成器と関連度ラベルは、レビュー済みの普通の Go コードです。どの数字を信頼する前にも——
私たちのものも含めて——「関連がある」が何を意味するかを確認してください。

2,419 セッション、179k メッセージ、1.9 GB のトランスクリプトを持つ実際のストアでの計測結果：

| 計測項目 | 結果 |
| --- | --- |
| プロセス内の検索 | 中央値 **0.7–0.8 ms**（`deja bench recall`、100 クエリ、半分はロシア語）、LongMemEval-S のヘイスタックでは約 19 ms |
| `deja <query>` のエンドツーエンド | そのストアで中央値約 0.2 秒：プロセス起動、全ストアの鮮度チェック、ランキング、出力を含む |
| 鮮度チェックのみ | 変更がない場合は約 50 ms |
| インデックスサイズ | 200 MB、コーパスの約 10% |

同じストアはその後 2,754 セッション、358k メッセージ、5.7 GB に増えました。
これに対するコールドなフルビルドは 71 秒かかり、232 MB のインデックスを書き出します——コーパスの 4% です。
トランスクリプトには繰り返しが多いため、割合は下がっていきます——そしてエンドツーエンドの中央値は
0.25 秒と変わっていません。

インデックスはインクリメンタルです。セッションファイルが増えたときは、そのファイルだけを読み直します。

## 仕組み

`~/.cache/deja` にあるローカルの転置インデックスです：JSONL と SQLite のストアを解析し、認証情報を
秘匿化し、`records.bin` とトークンバケットを書き出し、ファイルごとの状態を `manifest.gob` で追跡するので、
再実行時は変更分だけを取り込みます。MCP サーバー、stats、share、sync はすべてこのひとつのインデックスを
読みます。詳細は [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) を参照してください。

## FAQ

**何かがマシンの外に送られますか？** いいえ、あなたが指示しない限り送られません。
[データフロー](../../docs/SECURITY-MODEL.md#data-flows)を参照してください。

**すでにログに含まれているシークレットはどうなりますか？** それらは元のハーネスのファイル——つまり
エージェントのデータ——に残ります。`deja secrets` がそれを含むセッションを示すので、ローテーションや削除が
できます。`--scrub` は届く範囲のトランスクリプトを書き換えます。既知の形式——AWS キー、`api_key=`/`token=` の代入、Bearer トークンや裸の JWT、PEM ブロック、
各プロバイダーのトークン、高エントロピー値——はインデックス作成時に取り除かれるので、ダイジェスト、共有、
同期エクスポートには含まれません。パターンマッチングはシークレット検出ではありません：未知の形式は
すり抜ける可能性があります。[セキュリティモデル](../../docs/SECURITY-MODEL.md#redaction)を参照してください。

**エージェントが遅くなりませんか？** リコールはローカルインデックスに対する字句検索です：
中央値 0.7–0.8 ms で、モデルの応答を待つことはありません。フックではこれに加えてプロセス起動と
ストアの鮮度チェックがかかります——数 GB のストアで数十ミリ秒です。

**作業のやり方を変える必要はありますか？** いいえ。エージェントが自分でリコールを呼び出し、
自動リコールを有効にしていれば、セッションを開いた時点でプロジェクトの過去の決定をすでに知っています。

**他のメモリツールとどう違うのですか？**

| | deja | メモリプラットフォーム<br>（Mem0、Letta、memU） | セッション検索<br>（cass） |
| --- | :-: | :-: | :-: |
| インストール前の作業を知っている | はい | いいえ | はい |
| キャプチャ手順 | 不要、トランスクリプトがそのまま記憶 | エージェントやあなたのコードが事実を書き込む | 不要 |
| LLM や埋め込みのキーが必要 | いいえ | はい | 任意 |
| 頼まれなくてもリコールする | セッション開始時とツール実行前 | いいえ | いいえ |

[engram](https://github.com/Gentleman-Programming/engram) は記録先行型ツールの中で最も優れており、
そのモデルが合うなら試す価値があります。ただし、やはり空の状態から始まり、エージェントが保存することを
選んだものしか知りません。[完全な比較](https://vshulcz.github.io/deja-vu/guide/compare.html)では
11 のツールを取り上げています。

**Claude Code のセッション履歴はどこに保存されていて、検索できますか？**
`~/.claude/projects` の下に、セッションごとに 1 つの JSONL ファイルとして保存されています。Codex は
`~/.codex/sessions`、Cursor は SQLite の `state.vscdb` に保存します。`deja search` はそれらすべてをその場で
読み取り、`deja last` は全エージェントの最近のセッションを一覧表示し、`deja view` は履歴全体をひとつの
ローカルページとして開きます。各エージェントのパスは[セッションの保存場所](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)を参照してください。

**Claude Code のセッション履歴が消えました。もう戻りませんか？** Claude Code は 30 日より古い
トランスクリプトを削除します（`~/.claude/settings.json` の `cleanupPeriodDays`）。`claude --resume` が
一覧表示するのは残っているものだけです。削除前に deja がインデックス化したセッションは、ファイルが
消えた後も検索できます。詳細：[ディスク上のセッションファイル](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html)。

**Windows はどうですか？** ビルドは存在し、CI でもテストスイートを実行しています。十分に実績があるのは
macOS と Linux です。実環境でのレポートは [#9](https://github.com/vshulcz/deja-vu/issues/9) で歓迎しています。

**すべてを消去するには？**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## ガイド

機能ごとではなく、状況ごとに書かれています：

- [エージェントは以前の会話を覚えているか？](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — 各エージェントがセッション間で何を保持し、何を捨てるか
- [ディスク上のセッションファイル](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — `~/.claude/projects` はどのくらい大きくなるか、削除すると何を失うか
- [エージェントがコンテキストを失った](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — クラッシュ、クリア、空で戻ってきたセッションの後に
- [コンテキストウィンドウがいっぱい](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — コンパクションが何を残すかの計測と、代わりにすべきこと
- [昨日のセッションを再開する](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — 全エージェントから探し出し、それを持つエージェントで再開する
- [エージェントが修正済みのミスを繰り返す](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [問題を解決したセッションを見つける](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [エージェントがセッション間で忘れる理由](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [各エージェントの履歴の保存場所](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [コンパクションで失われるもの](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [エージェントの切り替え](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [エージェントの監査](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [会話のエクスポート](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [マシン間での利用](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [記憶のコスト](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

ハーネス別：[opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [DeepSeek Harness](https://vshulcz.github.io/deja-vu/guide/memory-for-dsh.html) · [Kimi Code](https://vshulcz.github.io/deja-vu/guide/memory-for-kimi.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [Qwen Code](https://vshulcz.github.io/deja-vu/guide/memory-for-qwen.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi と omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## 自分の履歴で試す

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

インストールに 10 秒、インデックス作成に約 10 秒。次にエージェントがセッションを開くとき、
そのプロジェクトで解決したことをすでに知っています——deja をインストールする前のことも含めて。

## コントリビュート

`make build test lint` を実行してから、[CONTRIBUTING.md](../../CONTRIBUTING.md) を読んでください。ハーネスの追加は
[パーサーレジストリ](../../docs/ARCHITECTURE.md#source-parsers)から始めます。優先事項と対象外の事項は
[ROADMAP.md](../../ROADMAP.md) にあります。初心者向けの Issue にはラベルが付いています。

## サポート

バグや質問は [Issues](https://github.com/vshulcz/deja-vu/issues) へどうぞ。
悪用可能だと思われるものは、代わりに [SECURITY.md](../../SECURITY.md) にある非公開のアドバイザリリンクから
報告してください。deja が何を読み、何をどこにも送らないか、そしてプロジェクトを除外したりセッションを
忘れさせたりする方法は[プライバシー](#プライバシー)にまとめています。

## ライセンス

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
