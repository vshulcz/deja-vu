deja indexes the session files your other coding agents already wrote to disk —
Claude Code, Codex, Cursor, opencode and more — and answers from them over MCP,
including sessions from before it was installed. No LLM, no embeddings, nothing
leaves the machine.

If deja is already installed — the install script, Homebrew, `go install` — the
extension uses that binary, and you keep it current the way you always did.
Otherwise the first start downloads a release build into the extension's own
directory, about 12 MB. On a slow connection that download
can outlast the sixty seconds Zed allows a context server to answer, and the
server is reported as timed out; starting it again uses the downloaded copy and
connects immediately.

A downloaded copy is checked against the current release about once a week, and
any failure in that check keeps the copy already on disk — an old deja answers,
an unreachable GitHub does not.

To name a specific binary:

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

The first query builds the index over whatever history is already on the
machine, which takes a few seconds; later queries answer in about a
millisecond.
