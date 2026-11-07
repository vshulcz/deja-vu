# dsh-deja

[English](../README.md) | 中文

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

属于 **[deja-vu](https://github.com/vshulcz/deja-vu)** 项目（LongMemEval-S **97.2% R@5** · **13.6 秒**索引 19,195 个会话 · Token 用量仅为 agentmemory 的**一半**）· [官网与在线搜索](https://vshulcz.github.io/deja-vu/) · [浏览器在线演示](https://vshulcz.github.io/deja-vu/view/) · **[★ 在 GitHub 上给 deja-vu 点 Star](https://github.com/vshulcz/deja-vu)**

DeepSeek Harness 自带的 `session-query` 已经可以检索它自己的会话。这个插件回答的是另一个问题：你在这台机器上**其他**智能体里做过什么。

deja 索引 Claude Code、Codex、Cursor、opencode、Antigravity、Grok Build、Kimi、Cline、Zed 等其他四十二个智能体本来就写在磁盘上的会话文件。不需要提前录制：历史已经在那里，包括安装 deja 之前的那些月份。如果你上周才转到 dsh，上周之前做过的事仍然可以在 dsh 里查到。

## 安装

```sh
dsh plugin --profile web add dsh-deja
```

也可以不走 npm，直接从仓库安装——根目录的 manifest 会重新导出这个目录，两种写法都行：

```sh
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

插件调用 `deja` 可执行文件。它作为依赖随插件一起安装；如果 `PATH` 上已有 `deja` 就直接使用，`DEJA_BIN` 可覆盖两者。

装了 CLI 的话，`deja install --auto` 也会把 dsh 接好——写入 deja 的 MCP 服务端和 `/deja` 命令——那条路更短。
两边都装也没问题：插件会检查 `$DSH_HOME/plugins/deja/` 下安装器写了什么，只补缺的部分，工具和命令不会注册两次。

## 提供的能力

六个模型可调用的工具：

| 工具 | 回答什么 |
|---|---|
| `deja_recall` | 匹配报错文本、函数名、文件路径或命令行参数的历史会话。 |
| `deja_session` | 单个历史会话的完整摘要，用于需要当时的判断依据时。 |
| `deja_blame` | 在修改或删除某个文件之前，讨论过这个文件的历史会话。 |
| `deja_fix` | 这台机器上同样的报错之后跑过什么。 |
| `deja_how` | 构建、测试或部署在这台机器上真实的执行命令。 |
| `deja_remember` | 把一条已确定的决定存下来，供以后召回。 |

一个命令：`/deja <要查什么>`。

以及默认开启的自动召回：每个会话一次，插件把这个项目里以前的会话定下来的事加进来；每一步之前，插件会问 deja 这台机器的历史能否回答当前提问，并把结果加入运行时上下文。多数情况下它保持沉默，只有确实有内容时才开口。

同一套召回也接上了 `deja install dsh-auto` 接的那些：读、改、写一个文件之后，模型会拿到以前的会话对这个文件说过什么；命令失败之后，会拿到这台机器上次遇到同样错误后跑过的命令；压缩时保留会话正在做的事；会话结束时撤掉它的在线标记。

在 `dsh web` 里，你也能看到 deja 在 Claude Code 里给人看的那几行：某个提问由历史回答时的回执（写明来自哪个智能体、多久以前），以及会话开始时召回了什么、首次建索引的说明和每周小结。它们显示在输入框上方的小面板里，直到下一次提问或你手动关闭。模型收到的内容与之前完全相同，这几行不会进入模型上下文。tui、headless 和 sdk 配置没有页面，因此没有变化：这些说明仍然随 `/deja` 的回复一起显示。等待页面超过十分钟的行会被丢弃。`deja install dsh-auto` 用同一份文件写出同一个面板；两者都装时也只有一个面板。

```yaml
- insert:
    - id: deja
      name: dsh-deja
      config:
        autoRecall: false   # 保留工具、/deja 和手动搜索
```

## 搜索控制

打开 **设置 → Deja**，可分别控制三个默认开启的开关：

- **Ctrl+F in conversations（会话内快捷搜索）**：把字面文本
  `deja search --session <不带 session- 前缀的会话 UUID> ` 填入消息输入框，
  后接当前草稿作为查询内容，并聚焦输入框。它不会自动发送消息。
  Windows/Linux 桌面端使用 **Ctrl+F**，macOS 桌面端使用 **Cmd+F**；
  浏览器中 Windows 使用 **Ctrl+Alt+F**，macOS 使用 **Option+Command+F**，
  因为 DSH 为浏览器原生查找保留了普通的 Find 快捷键。
- **Ctrl+K opens Deja full-text search（Ctrl+K 唤起 Deja 全文搜索）**：接管
  Ctrl+K 打开的侧栏搜索——打开后直接进入 Deja 全文模式，而不是原来的标题
  搜索；点放大镜可对当次打开恢复原生搜索。由于 DSH 快捷键注册表不允许第二个
  Ctrl+K 默认绑定，此开关同时绑定 **Ctrl+Alt+K**（浏览器中为
  Ctrl+Option+Shift+K）作为直达 Deja 搜索的快捷键。关闭此开关即完全恢复
  官方行为。
- **Deja search in the sidebar（侧栏 Deja 搜索）**：先打开侧栏搜索，
  再点一次放大镜，切换到 Deja 全文搜索。输入关键词后，只检索本地索引中的
  **DSH 会话**，最多显示 20 条结果及匹配片段；点击结果可打开对应会话。
  再点放大镜可恢复普通侧栏结果。若还有更多匹配，请缩小查询范围。
  Deja 不可用、搜索失败或无法打开会话时，界面会显示错误信息。

两个偏好分别保存在当前浏览器／配置的本地存储中，互不影响，也不会同步到其他
浏览器或设备。即使设置了 `autoRecall: false`，手动搜索仍然可用。

### 自定义快捷键

两条 Deja 命令都是普通的 DSH 快捷键命令，可以在内置的快捷键编辑器里修改：
打开 **设置 → 通用 → 快捷键**（桌面端也可按 **Ctrl+/**），在「应用操作」
分组里找到两条 **Deja** 条目，点击即可录制新组合键或清除绑定。修改按平台
保存在 DSH 自己的键位文件里，插件升级不会覆盖；上面的开关控制命令是否生效，
与键位无关。官方的 `session.search`（Ctrl+K）绑定不受影响——想换打开标题
搜索的按键，也在这里改。

侧栏全文搜索需要安装 **dsh-deja 包的宿主插件**。CLI 的
`deja install dsh-auto` 会复制浏览器面板，但目前不会安装搜索接口；
请按上面的步骤安装包以启用接口。原有的 CLI 工具、命令可以与包同时使用。

## 接入方式

召回通过 `ctx.systemPrompt.context` 注入，该回调在每次 assembly 时求值。另一种看起来可行的做法——在 `agent/pre-step` 中间件里往这一步的消息列表里插入一条消息——插件能加载、回合也能正常结束，但内容到不了模型：该 waterfall 中后续的监听器会基于 payload 重建返回值，插入的消息被丢弃且没有任何报错。这一点是通过读取 dsh 0.1.1-rc.2 实际发出的请求确认的。

手动侧栏搜索使用 DSH 同一套登录保护下的 `GET /api/deja-search` 接口，
通过异步、无 shell 的调用执行
`deja search --harness deepseek --json --limit 20 -- <query>`，
超时为 20 秒，输出上限为 2 MiB。空查询不会启动进程；超过 500 个字符的查询会被拒绝。
接口只返回精简的会话元数据和匹配片段，不返回完整对话。

DSH 目前没有侧栏搜索 provider API。浏览器适配器限定在
`data-slot="sidebar.workspaces"` 锚点内，通过带 `aria-expanded` 的语义化
搜索按钮和 `_listArea` CSS 类后缀定位原界面。已检查的界面版本为
**DSH 0.2.1-rc.5**。如果旧版或变更后的布局缺少这些元素，适配器会保持普通
侧栏不变。这些内部标记依赖可能需要随 DSH 后续版本调整。

## 不做什么

不用大模型，不用向量嵌入。索引是本地 BM25，建立在本来就存在的文件之上，一次查询约一毫秒，除非你主动要求，否则不走网络。密钥在建立索引时被脱敏。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。MIT 许可。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
