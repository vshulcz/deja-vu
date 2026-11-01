# deja for Devin

A Devin plugin that gives the agent your own past sessions as memory: what you
worked on before, in this project and others, from Devin CLI and from the
other coding agents on the machine.

[deja](https://github.com/vshulcz/deja-vu) indexes the session stores coding
agents already write to disk, including sessions from before it was installed,
and answers from them locally: BM25 over the transcripts, no model and no
embeddings. Credentials are redacted as the index is built.

## What the plugin adds

- The `deja` MCP server: search past sessions, open one as a digest, see the
  sessions behind a file, the commands that fixed an error before, the real
  build and test commands this machine runs.
- The `deja-history` skill, which tells the agent when to use it.
- Hooks: a digest of recent related work at session start and after a
  compaction, recall on each prompt, the file or command's prior decision
  after a tool call, the fix that worked last time after a failed `exec`, and
  a marker when the session ends.

## Install

The plugin calls the `deja` binary, which is not in the bundle:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Then install the plugin from this subdirectory of the repo:

```sh
devin plugins install vshulcz/deja-vu#devin-plugin
```

Without the binary the hooks stay silent and say once, at session start, how
to get it.

`deja install devin-auto` writes the same server and hooks into
`~/.config/devin` from the command line instead (`deja install devin` writes
the server alone), and it is the recommended path on Devin CLI: everything the
plugin does, it does natively. If both are present the plugin's hooks stand
down — they see the wiring in `~/.config/devin/config.json` and exit — so
nothing runs twice.

## Where this runs

On Devin CLI the hooks fire on every event the plugin wires — verified on
Devin CLI 3000.11.3. Devin's cloud sessions are reported to fire plugin hooks
on a smaller event set (`SessionStart` and `SessionEnd` among the ones not
delivered — the plugin docs are a closed beta and the list is not public), so
on those the opening digest and the end-of-session marker may be CLI-only;
the prompt, tool and compaction hooks work on both. Either way the MCP
server and the skill are always available.

deja reads Devin CLI's sessions from `~/.local/share/devin/cli/sessions.db`
(the older `cli_sessions.db` is still read), including the subagent runs it
keeps as side chains and the summaries its compactor writes beside the store.
