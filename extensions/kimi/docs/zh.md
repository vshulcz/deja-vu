# Kimi Code 上的 deja

[English](../README.md) | 中文

[deja](https://github.com/vshulcz/deja-vu) 索引编程智能体本来就写在磁盘上的会话文件——Claude Code、Codex、Cursor、opencode 等其他三十三个智能体——并据此回答问题。这个插件把那个索引带进 Kimi Code：召回随提问一起到达，智能体也可以自己检索历史。

## 安装

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

这种写法会锁定最新的发布版并记录来源，而这正是 Kimi 的更新检查所读取的内容。还有一个 `kimi-deja.zip` 发布资源——16 KB，而不是 3 MB 的整个仓库——适合市场条目，或者不希望拉下整个项目的安装方式。

## 更新

Kimi 只会提示从它自己市场安装的插件有更新。对于从仓库安装的情况，再跑一次 `/plugins install` 就会拉取当前的发布版；而当你手上这份落后于这个 deja 附带的版本时，`deja doctor` 会说明：

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.1.0 installed, v0.2.0 ships with this deja — reinstall it in Kimi to update)
```

如果还没有可执行文件，装一下：

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

插件从托管副本加载，所以装完之后请执行 `/reload` 或 `/new`。

## 提供的能力

- **每条提问都有召回。** 一个 `UserPromptSubmit` 钩子运行 `deja hook-prompt`，Kimi 把找到的内容附加到本回合。没有匹配时保持沉默。
- **工具。** 插件把 `deja mcp` 声明为 MCP 服务端：一个 `deja` 工具，模式可以是 `recall`、`context`、`blame`、`fix`、`how`、`orient` 或 `remember`。
- **`/deja:recall <要查什么>`**，直接检索历史。
- **`deja-history` 技能**，会话开始时加载，让智能体知道在重新调试之前先去查一查。

## 用哪个可执行文件

顺序是：`DEJA_BIN`、你自己装的 deja（`~/.local/bin`、`/usr/local/bin`、`/opt/homebrew/bin`），然后是 `PATH` 上的 `deja`。你自己的 `deja update` 或 `brew upgrade deja` 会盖过插件发布时锁定的版本。

## 如果你也跑过 `deja install kimi`

CLI 会把同样的 MCP 服务端写进 `~/.kimi-code/mcp.json`，把同样的钩子写进 `config.toml`。Kimi 在自己的命名空间下运行插件的服务端（`plugin-deja:deja`），所以两份都会跑：每个工具列两遍，召回也附加两遍。

这个插件会检查安装器写下的接线，发现之后就主动让位——`deja install` 保持更新的是 CLI 那一份。`deja uninstall kimi` 把所有权交回插件，中间不需要重装：这个检查发生在钩子触发和服务端启动的时候，而不是安装时做一次就算。

当接线由插件承担时，`deja doctor` 对 kimi 显示 `plugin`，所以只装了插件的机器不会被读成没接好。

有两种情况它看不到：你手工添加、名字不叫 `deja` 的 MCP 条目（它会与这一份并存），以及你自己写的、调用 deja 但没有安装器标记注释的钩子。

## 许可

MIT

属于 [deja-vu](https://github.com/vshulcz/deja-vu) 项目。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
