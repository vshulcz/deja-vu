# deja for CodeBuddy Code

A CodeBuddy Code plugin that gives the agent your own past sessions as memory:
what you worked on before, in this project and others, from CodeBuddy and from
the other coding agents on the machine.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
coding agents already write to disk, including sessions from before it was
installed, and answers from them locally: BM25 over the transcripts, no model
and no embeddings. Credentials are redacted as the index is built.

## What the plugin adds

- The `deja` MCP server: search past sessions, open one as a digest, see the
  sessions behind a file, the commands that fixed an error before, the real
  build and test commands this machine runs.
- The `deja-history` skill, which tells the agent when to use it.
- Hooks: a digest of recent related work at session start, recall on each
  prompt, what past sessions settled about a file after it is read or edited,
  the fix that worked last time after a failed `Bash` or `PowerShell` command,
  a capture before compaction, and a marker when the session ends.

## Install

The plugin calls the `deja` binary, which is not in the bundle:

```sh
brew install deja-vu
```

or

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Then install the plugin from the CodeBuddy marketplace. Without the binary the
hooks stay silent and say once, at session start, how to get it.

`deja install codebuddy-auto` writes the same server and hooks into
`~/.codebuddy` from the command line instead (`deja install codebuddy` writes
the server alone). If both are present the plugin's hooks stand down, so
nothing runs twice.

On Windows the plugin's hooks and server are shell scripts, so they need Git
for Windows. Without it, use `deja install codebuddy-auto`, which writes hooks
that run through PowerShell.

deja reads CodeBuddy's own sessions from `~/.codebuddy/projects`
(`CODEBUDDY_CONFIG_DIR` is honoured) and WorkBuddy's from `~/.workbuddy/projects`.

## Privacy

Indexing and search are local: deja reads the transcripts in place and keeps
its index under `~/.cache/deja`. The network is used only by `deja update`,
`deja sync ssh`, the version check in `deja doctor`, `deja embed` against an
endpoint you configure, and a once-a-day release check from a command typed at a
terminal (`DEJA_OFFLINE=1` turns it off). Details are in the
[repository README](https://github.com/vshulcz/deja-vu#readme); security policy in
[SECURITY.md](https://github.com/vshulcz/deja-vu/blob/main/SECURITY.md).

MIT, same as deja.
