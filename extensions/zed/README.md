# deja for Zed

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/zed/docs/zh.md)

[deja](https://github.com/vshulcz/deja-vu) indexes the session files that
coding agents already write to disk — Claude Code, Codex, Cursor, opencode and
thirty more — and answers from them. This extension serves that index to Zed's
agent panel as a context server, so a thread can search what you did before,
including work from before deja was installed.

## What the agent gets

One tool, `deja`, called with a `mode`:

| Mode | Answers |
|---|---|
| `recall` | Sessions matching an error string, function name, file path or flag. |
| `context` | The full digest of one past session, when the reasoning behind it matters. |
| `blame` | The sessions that discussed a file, before you edit or delete it. |
| `fix` | What this machine ran after that same error before. |
| `how` | The real invocation for a build, test or deploy, from what ran here. |
| `orient` | The commands past sessions ran in this project and the files they worked in. |
| `remember` | Stores one durable decision for later recall. |

## Install

Install the extension from Zed's extension list. On first use it downloads a
release build of the `deja` binary into its own directory — it cannot use a deja
you already have unless you name it, because an extension runs sandboxed and the
usual install paths are not reachable from inside it:

`deja install --auto` reaches Zed too, and writes the server into
`settings.json` directly — the shorter path when you have the CLI. Both use the
same id, `deja-context-server`, and Zed keys servers by id, so either order
leaves exactly one server: the installer leaves this extension's entry alone
when it finds it, and installing the extension later lands on the same key
rather than beside it. `deja uninstall zed` removes what the CLI wrote.

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

Indexing and search are local: no network calls, and credentials are redacted
as the index is built.

MIT, same as deja.
