# deja for Zed

[deja](https://github.com/vshulcz/deja-vu) indexes the session files that
coding agents already write to disk — Claude Code, Codex, Cursor, opencode and
sixteen more — and answers from them. This extension serves that index to Zed's
agent panel as a context server, so a thread can search what you did before,
including work from before deja was installed.

## What the agent gets

| Tool | Answers |
|---|---|
| `recall` | Sessions matching an error string, function name, file path or flag. |
| `recall_context` | The full digest of one past session, when the reasoning behind it matters. |
| `blame` | The sessions that discussed a file, before you edit or delete it. |
| `fix` | What this machine ran after that same error before. |
| `how` | The real invocation for a build, test or deploy, from what ran here. |
| `remember` | Stores one durable decision for later recall. |

## Install

Install the extension from Zed's extension list. On first use it downloads a
release build of the `deja` binary into its own directory; if you already have
one, point at it:

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

Indexing and search are local. Nothing is sent anywhere, and credentials are
redacted as the index is built.

MIT, same as deja.
