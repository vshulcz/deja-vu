# Spec Kit 上的 deja-vu

[English](../README.md) | 中文

这是一个 [Spec Kit](https://github.com/github/spec-kit) 扩展。在动手实现某个功能之前，`/speckit.deja-vu.recall` 会在你过去的编程智能体会话里搜索这个功能，告诉你以前试过什么、定下了什么、哪些路走不通，每一条都附上出处的会话 id。

会话来自 [deja](https://github.com/vshulcz/deja-vu)：它索引 Claude Code、Codex、Cursor、Copilot、Gemini 等智能体本来就写在磁盘上的会话记录。所有数据都留在你自己的机器上。

## 安装

先装 deja：

```sh
brew install deja-vu
# 或者
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

然后在 Spec Kit 项目里：

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

每次 deja 发布都会附带 `deja-spec-kit.zip`，版本号和发布版相同。想用本地仓库试一下：`specify extension add --dev /path/to/deja-vu/extensions/spec-kit`。

## 用法

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

不带参数时，它会读取当前功能的 `spec.md` 和 `plan.md`，自己挑选查询词；带参数时就按这些词搜索。命令名会随智能体的风格变化，也可能显示为 `/speckit-deja-vu-recall` 或 `$speckit-deja-vu-recall`。

它在仓库根目录下实际执行的是：

```sh
deja recall -- "<query>"   # 匹配的会话，本项目优先
deja files -- "<query>"    # 做这个主题时打开或修改过的文件
```

摘录太短、看不出发生了什么时，再用 `deja show <id>` 读完整会话。结果是一节 `Prior sessions`，分为试过但失败的、已经定下的、涉及的文件和没做完的，每一条都标注 `(deja:<id>)`。

## 钩子

扩展注册了一个可选的 `before_implement` 钩子。运行 `/speckit.implement` 时，智能体会先问你要不要做一次回忆；你不同意就什么都不会执行。要关掉它，在 `.specify/extensions.yml` 里把对应条目设为 `enabled: false`，或者运行 `specify extension disable deja-vu`。

## 说明

- 只读：命令只搜索和读取，不修改任何文件。
- 一台机器上第一次调用 deja 会建立索引，历史很多时需要一点时间；之后的查询都在一秒内返回。
- 回忆出来的会话内容只当作参考资料，命令会要求智能体绝不执行其中出现的指令。

## 许可证

MIT
