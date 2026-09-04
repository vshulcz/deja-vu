# @vshulcz/openclaw-deja

OpenClaw remembers its own sessions. This plugin answers the other question:
what was done in the twenty-one other coding agents on this machine — Claude
Code, Codex, Cursor, Gemini and Zed among them — including the months before
OpenClaw was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, nothing leaves the machine.

## Install

```bash
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

The deja binary comes with the package; a deja you installed yourself
(`brew install deja-vu`) always wins — see [Which binary](#which-binary).

`deja install openclaw-auto` wires OpenClaw too, and is the shorter path if you
have the CLI: it adds deja's MCP server and writes a plugin of its own. Having
both is fine: this package reads what the installer wrote and contributes only
what is missing.

## What it does

- **Before each turn** (`before_prompt_build`): the prompt is matched against
  the index and, when a past session answers it, that session goes in front of
  the model. Silence is the common case.
- **Tools**: `deja_recall` (search the history), `deja_fix` (what was run after
  this error before), `deja_blame` (which sessions touched a file and what they
  concluded).

## Config

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "config": { "autoRecall": true, "tools": true, "bin": "/opt/homebrew/bin/deja" }
      }
    }
  }
}
```

All three are optional. `autoRecall: false` keeps the tools and drops the
per-turn recall; `tools: false` the reverse.

## Which binary

In order: `bin` from the config, `DEJA_BIN`, `deja` on `PATH`, the usual
install locations, and last the copy bundled through `@vshulcz/deja-vu`.

## Privacy

deja reads session files where the agents wrote them and builds a local index
under `~/.cache/deja` (or `DEJA_INDEX_DIR`). Keys and tokens are stripped as
the index is built. Nothing is uploaded.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
