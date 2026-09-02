# opencode-deja

opencode remembers its own sessions. This plugin answers the other question:
what you did in the twenty-one other coding agents on this machine — Claude Code,
Codex, Cursor, Gemini and Zed among them — including the months before you
installed anything.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, nothing leaves the machine.

## Install

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": ["opencode-deja"]
}
```

opencode installs the package on start. The deja binary comes with it, but a
deja you installed yourself always wins — see [Which binary](#which-binary).

`deja install --auto` wires opencode too, and is the shorter path if you have
the CLI: it adds deja's MCP server and writes a plugin of its own. Having both
is fine. This package reads what the installer left — the MCP entry in
`opencode.json`, the plugin file in `plugins/deja.js` — and contributes only
what is missing, so nothing is registered or recalled twice.

## What you get

Six tools the model can call:

| Tool | Answers |
|---|---|
| `deja_recall` | past sessions matching an error, function, file or flag |
| `deja_session` | the full digest of one past session — what was tried and decided |
| `deja_blame` | the sessions that discussed a file, before you edit it |
| `deja_fix` | what this machine ran after that same error, where it stayed fixed |
| `deja_how` | the real build/test/deploy invocation, with the flags actually used |
| `deja_remember` | store one durable decision |

And recall nobody has to ask for:

- the project's recent sessions are pushed onto the system prompt once per
  session, with a one-time toast so you know memory arrived;
- each prompt gets a relevance pass of its own — silent when nothing matches;
- before compaction the working transcript is indexed, so the session survives
  the window collapsing.

## Options

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

- `autoRecall` (default `true`) — the three hooks above. Turn off to keep only
  the tools.
- `tools` (default `true`) — the six tools. They are skipped on their own when
  `deja install` already wired the MCP server; set this to `false` to drop them
  when you reach deja some other way.
- `bin` — path to a specific deja binary.

## Which binary

In order: `bin` from the options, `DEJA_BIN`, a `deja` on `PATH`, the usual
install locations (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`), and
only then the copy npm installed with this package. Your own `deja update` or
`brew upgrade deja` wins over whatever version this package shipped with.

Without deja anywhere, the tools say so instead of reporting an empty history,
and the hooks stay quiet.

## License

MIT
