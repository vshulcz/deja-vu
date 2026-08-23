<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img src="assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>给编程智能体的记忆，从你已经有的历史开始。</b></p>

<p align="center">你的智能体正准备重新调试一个你三月份就修好的问题。deja 索引 Claude Code、Codex、Cursor
以及这台机器上其他所有智能体本来就写在磁盘上的会话，并在需要时把对的那一条交回来。</p>

<p align="center"><img src="assets/demo.gif" width="720" alt="同一个问题问同一个智能体两次：没有记忆时它毫无印象，有 deja 时它用八个月前的结论作答"></p>

<p align="center"><sub><em>没有人去搜索——是智能体自己调用了 deja。每一行都引自两个真实会话。</em></sub></p>

<p align="center"><b>其他记忆工具都从空白开始，往后记录。deja 一开始就是满的。</b></p>

<p align="center">
LongMemEval-S 上 <b>85.3% hit@1</b> &middot; LoCoMo 上 <b>69.6%</b> &middot; 5&nbsp;GB 历史上的查询在<b>亚毫秒</b>级<br>
<sub>两套评测都在本仓库里，几分钟即可在公开数据集上跑完 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">自己核对这些数字</a></sub>
</p>

<p align="center"><a href="README.md">English</a> | 中文</p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">文档</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">评测</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">与同类对比</a></p>

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

安装十秒，建索引约十秒，然后就能用了。第二条命令会把 MCP 召回接到它找到的每一个智能体上，
在支持的地方打开会话启动时的召回，并建好第一份索引，这样下一次会话不必再等。

打开一个新的智能体会话，问一件几个月前做过的事：

> 我们以前处理过 jwt refresh rotation 吗？查一下你的记忆

也不必特意去问——开启自动召回后，会话一打开，智能体就已经知道你在这个项目里解决过什么。

DeepSeek Harness 用户可以直接装插件：

```sh
dsh plugin --profile web add dsh-deja
```

它带来三个模型可调用的工具（`deja_recall`、`deja_session`、`deja_blame`）、`/deja` 命令，
以及可选的自动召回。详见 [packages/dsh-deja](packages/dsh-deja/README.zh.md)。

其他安装方式：`brew install vshulcz/tap/deja-vu`、
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`，或者用
`npx @vshulcz/deja-vu "查询词"` 先试试而不装任何东西。Windows 上安装脚本会退出并提示
`unsupported OS`——它是 shell 脚本，请从
[最新发布](https://github.com/vshulcz/deja-vu/releases/latest)取
`deja-vu_<version>_windows_amd64.zip`，把 `deja.exe` 放进 `PATH`。

只有二进制文件也是一次完整安装：索引、搜索、`show`、`ctx`、`blame`、`--json`
和脱敏都不需要别的东西。`deja install` 负责的是把 MCP 接进你的智能体、打开会话启动召回——值得有，但可选。

## 能得到什么

**在 Codex 里解决，Claude 记得。** 二十个编程智能体把每一次对话都写进本地文件，
deja 把这些文件变成一层它们都能读的记忆。

| | |
| --- | --- |
| **回溯式搜索** | `deja "connection pool exhausted"` 搜遍几个 GB，包括你安装 deja 之前的一切。自然语言提问会退化到相关性档位。时间是提示，不是过滤条件。 |
| **跨智能体召回** | MCP 的 `recall` 工具在任何一个智能体里都能回答「这个我们三周前修过」，不管当初是谁修的。 |
| **压缩之后仍然在** | 在 43 次上下文压缩上实测：摘要保住了 77% 的决策和 0.2% 的你跑过的命令。其余 99.8% 由 deja 交回。 |
| **在动手的那一刻召回** | 智能体改文件或跑命令之前，`PreToolUse` 钩子会说出这个文件此前的决定、或这条命令能用的写法。命令失败时，`PostToolUse` 钩子给出这台机器上同样报错之后跑过什么——那正是智能体不会主动去问的一对。 |
| **索引的是活儿，不只是话** | 每一轮打开过的文件、跑过的命令及其退出码、以及一次编辑替换掉的确切片段。那正是所有摘要都会丢掉的部分。 |

还有：`deja promote <id> --state rejected` 标注被推翻的决定，此后每一次命中都会显示它试过并被否决；
命中会报告「本会话涉及的 4 个文件此后已变更」，判断不了时就不说；
`deja sync ssh laptop` 在机器之间搬运记忆，只追加、中间没有云；
`deja handoff --to codex` 把当前上下文打包，好在另一个智能体里接着做；
密钥、令牌、JWT 和私钥块在建索引时就被剥掉。

## 隐私

建索引和搜索都在本地。只有 `deja update`、`deja sync ssh` 和 `deja doctor` 里的版本检查会用到网络。

凭据在建索引时脱敏：AWS 密钥、`api_key=` 与 `token=` 赋值、bearer 令牌与裸 JWT、PEM 私钥块、
各家提供商的令牌、`scheme://user:pass@host` 形式的 URL，以及没有规则能匹配的高熵值。
值会变成 `[redacted:<kind>]`，周围的文本仍然可搜。`deja share` 和 `deja sync export` 在导出时再做一次脱敏。

`deja forget` 把会话从重建后的索引里移除并写下墓碑，之后的 `deja index` 无法从原始历史里把它们恢复回来。
[安全模型](docs/SECURITY-MODEL.md)记录了数据流向、脱敏的边界、信任假设与发布验证。

## 命令行

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| 命令 | 作用 |
| --- | --- |
| `deja <查询词>` | 搜索所有历史。多个词是 AND，引号内要求连续文本；没有精确命中时会尝试词形与近似拼写。 |
| `deja blame <路径>` | 哪些会话讨论过这个文件、当时决定了什么、为什么。 |
| `deja files <主题>` | 反方向：某个主题的工作实际动过哪些文件。 |
| `deja how <工具>` | 这台机器实际怎么跑一件事，带真实参数，来自智能体此前跑过的命令。 |
| `deja fix <报错>` | 这台机器上同样的报错之后跑过什么，且那次之后错误没有再出现。 |
| `deja friction` | 命中三个以上不同会话的报错，并指出来自哪些工具。 |
| `deja ctx <查询词>` | 最佳命中的 Markdown 摘要，可直接接进提示词。 |
| `deja resume <id>` | 在原来的工具里重新打开找到的那个会话。 |
| `deja view` | 把整个记忆导出成一个本地 HTML 文件。没有服务端，数据不离开本机。 |
| `deja doctor [--deep]` | 自检；加 `--deep` 时用源文件验证索引。 |
| `deja mcp` | stdio 的 MCP 服务端，也就是 `deja install` 接进去的那个。 |

完整参考见[命令文档](https://vshulcz.github.io/deja-vu/guide/commands.html)。

### MCP 工具

服务端提供 `recall`、`recall_context`、`blame`、`fix`、`how` 和 `remember`。
`deja install` 会自动接好，只有手工配置智能体时才需要关心它们。

## 支持的工具

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · Roo Code · DeepSeek Harness · Zed。

每个工具分别支持 MCP 召回、自动召回、技能、命令、resume 和 handoff 中的哪些，见
[英文 README 的能力矩阵](README.md#supported-harnesses)。自定义存储位置通过 `DEJA_*_ROOT`
变量指定，各家自己的迁移变量也会被尊重。

## 可选的语义召回

用 `DEJA_EMBED_URL` 把 `deja embed` 指向本地的 Ollama、LM Studio 或任何 OpenAI 兼容端点，
换个说法提问也能命中。没有可用的运行时，词法搜索和 MCP 召回照常工作。

## 证据

```sh
deja bench recall     # 排序回归下限，召回下降时 CI 失败
deja bench context    # 30 条带种子的任务链，外加五个负对照
```

上下文实验把 deja 召回与全量历史、朴素 grep 和冷启动作对比。默认种子下：

| 方案 | token 中位数 | 覆盖率中位数 | 负对照 token |
| --- | ---: | ---: | ---: |
| deja-recall | 286 | 1.00 | 0 |
| full-history | 16,919 | 1.00 | 14,920 |
| naive-grep | 57,489 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

与直接 grep 原始日志相同的事实覆盖率，token 少约 200 倍；比完整重放命中的会话少约 60 倍；
在没有相关历史的链条上则什么都不注入。语料生成器和相关性标注是普通的、可审阅的 Go 代码。
在相信任何数字之前，先审清「相关」是怎么定义的——包括我们的数字。

在一份真实的仓库上测得：1,551 个会话、143k 条消息，跨九个工具共 5.2 GB。

| 指标 | 结果 |
| --- | --- |
| 进程内查询 | 中位数 **~0.4 ms**，LongMemEval-S 干草堆上约 25 ms |
| `deja <查询词>` 端到端 | 该仓库上中位数约 0.2 s：进程启动、对所有存储做新鲜度检查、排序、打印 |
| 仅新鲜度检查 | 没有变化时约 30 ms |
| 索引大小 | 160 MB，约为语料的 3% |

索引是增量的。会话文件变长时，只重新读那一个文件。

## 工作原理

`~/.cache/deja` 里的本地倒排索引：解析 JSONL 与 SQLite 存储、脱敏凭据、写出 `records.bin`
和词桶，并在 `manifest.gob` 里记录每个文件的状态，因此重复运行只摄入变化的部分。
MCP 服务端、统计、分享和同步都读这一份索引。细节见
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 常见问题

**有东西离开我的机器吗？** 没有，除非你主动要求。见[数据流向](docs/SECURITY-MODEL.md#data-flows)。

**日志里已经有的密钥怎么办？** 它们留在原本的工具文件里，那是你的智能体的数据。
它们不会进入 deja 的索引、摘要、分享或同步导出。

**会拖慢我的智能体吗？** 一次召回是对本地索引的词法查询：中位数 ~0.4 ms，没有任何东西在等模型。
钩子会额外加上进程启动和对存储的新鲜度检查——几个 GB 的仓库上是几十毫秒。

**我需要改变工作方式吗？** 不需要。是智能体自己调用召回；开启自动召回后，
会话一打开它就已经知道这个项目此前的决定。

**和其他记忆工具有什么不同？**

| | deja | 记忆平台<br>(Mem0、Letta、memU) | 会话检索<br>(cass) |
| --- | :-: | :-: | :-: |
| 知道安装它之前的工作 | 是 | 否 | 是 |
| 需要采集步骤 | 不需要，转录本身就是记忆 | 由智能体或你的代码写入事实 | 不需要 |
| 需要大模型或嵌入密钥 | 否 | 是 | 可选 |
| 不用问也会召回 | 会话开始时、以及工具执行前 | 否 | 否 |

[完整对比](https://vshulcz.github.io/deja-vu/guide/compare.html)覆盖了其中十一个。

**怎么全部清除？**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## 参与开发

`make build test lint`，然后看 [CONTRIBUTING.md](CONTRIBUTING.md)。
新增一个工具从[解析器注册表](docs/ARCHITECTURE.md#source-parsers)开始。
优先级与非目标见 [ROADMAP.md](ROADMAP.md)。

## 许可

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
