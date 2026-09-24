# Zed 上的 deja

[English](../README.md) | 中文

[deja](https://github.com/vshulcz/deja-vu) 索引编程智能体本来就写在磁盘上的会话文件——Claude Code、Codex、Cursor、opencode 等其他三十三个智能体——并据此回答问题。这个扩展把那个索引作为 context server 接到 Zed 的 agent 面板上，于是一个对话可以检索你先前做过什么，包括装 deja 之前的那些工作。

## 智能体拿到什么

一个工具 `deja`，用 `mode` 指定要做什么：

| 模式 | 回答什么 |
|---|---|
| `recall` | 匹配报错文本、函数名、文件路径或命令行参数的历史会话。 |
| `context` | 单个历史会话的完整摘要，用于需要当时的判断依据时。 |
| `blame` | 在修改或删除某个文件之前，讨论过这个文件的历史会话。 |
| `fix` | 这台机器上同样的报错之后跑过什么。 |
| `how` | 构建、测试或部署在这台机器上真实的执行命令。 |
| `orient` | 先前会话在这个项目里跑过的命令，以及它们改过的文件。 |
| `remember` | 把一条长期有效的决定存下来，供以后召回。 |

## 安装

在 Zed 的扩展列表里安装这个扩展。首次使用时它会把 `deja` 的发布版可执行文件下载到自己的目录里——除非你指名，否则它用不上你已经装好的那个 deja：扩展在沙箱里运行，常见的安装路径从里面够不着。

装了 CLI 的话，`deja install --auto` 同样能接上 Zed，并直接把服务端写进 `settings.json`——那条路更短。两边用的是同一个 id `deja-context-server`，而 Zed 按 id 索引服务端，所以无论先装哪一个，最后都只会有一个：安装器发现这个扩展的条目时不会去动它，之后再装扩展也会落在同一个键上，而不是并排多出一个。`deja uninstall zed` 会移除 CLI 写下的内容。

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

索引和检索都在本地：不发起网络请求，建立索引时凭据会被脱敏。

MIT 许可，与 deja 一致。觉得有用的话，欢迎在 [GitHub](https://github.com/vshulcz/deja-vu) 上给 deja-vu 点个 Star。
