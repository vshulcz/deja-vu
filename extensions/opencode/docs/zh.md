# opencode-deja

[English](../README.md) | 中文

opencode 记得自己的会话。这个插件回答的是另一个问题：你在这台机器上 Claude Code、Codex、Cursor、Gemini、Zed 等其他三十三个编程智能体里做过什么，包括你安装任何东西之前的那几个月。

插件运行 [deja](https://github.com/vshulcz/deja-vu)，一个本地 Go 可执行文件，索引这些智能体本来就写在磁盘上的会话记录。不用大模型，不用向量嵌入，除非你主动要求，否则不走网络。

## 安装

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["opencode-deja"]
}
```

opencode 启动时会安装这个包。deja 可执行文件随包一起提供，但你自己装的 deja 永远优先——见[用哪个可执行文件](#用哪个可执行文件)。

装了 CLI 的话，`deja install --auto` 也会把 opencode 接好，那条路更短：它写入 deja 的 MCP 服务端，并自己写一个插件。两边都装也没问题：这个包会读取安装器留下的东西——`opencode.json` 里的 MCP 条目、`plugins/deja.js` 里的插件文件——只补缺的部分，所以不会注册两次，也不会召回两次。

## 提供的能力

六个模型可调用的工具：

| 工具 | 回答什么 |
|---|---|
| `deja_recall` | 匹配报错文本、函数名、文件路径或命令行参数的历史会话。 |
| `deja_session` | 单个历史会话的完整摘要——当时试过什么、定下了什么。 |
| `deja_blame` | 在修改某个文件之前，讨论过这个文件的历史会话。 |
| `deja_fix` | 这台机器上同样的报错之后跑过什么，而且那次修好了。 |
| `deja_how` | 构建、测试或部署真实的执行命令，带上实际用过的参数。 |
| `deja_remember` | 存下一条长期有效的决定。 |

以及不需要谁开口的召回：

- 每个会话一次，把这个项目最近的会话推到系统提示里，并弹一次提示让你知道记忆已经到位；
- 每条提问都单独做一次相关性判断——没有匹配时保持沉默；
- 压缩之前把当前的会话记录索引一遍，于是上下文窗口塌缩后这次会话依然留得下来。

## 选项

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

- `autoRecall`（默认 `true`）——上面那三个钩子。关掉就只剩工具。
- `tools`（默认 `true`）——那六个工具。当 `deja install` 已经接好 MCP 服务端时它们会自行跳过；如果你通过别的方式用上 deja，把这一项设为 `false` 即可去掉。
- `bin` —— 指定某个 deja 可执行文件的路径。

## 用哪个可执行文件

顺序是：选项里的 `bin`、`DEJA_BIN`、`PATH` 上的 `deja`、常见安装位置（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`），最后才是 npm 随这个包装下的那份。你自己的 `deja update` 或 `brew upgrade deja` 会盖过这个包发布时冻结的版本。

哪里都没有 deja 时，工具会直接说明情况，而不是报告一段空历史；钩子则保持沉默。

## 许可

MIT

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
