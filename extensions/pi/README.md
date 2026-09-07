# @vshulcz/pi-deja

pi remembers its own sessions. This extension answers the other question: what
was done in the twenty-three other coding agents on this machine — Claude Code,
Codex, Cursor, Gemini, OpenClaw and Hermes among them — including the months
before pi was installed.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, nothing leaves the machine.

## Install

```bash
pi install npm:@vshulcz/pi-deja
```

The deja binary comes with the package; a deja you installed yourself
(`brew install deja-vu`) always wins — see [Which binary](#which-binary).

`deja install pi-auto` wires pi too, and is the shorter path if you have the
CLI: it adds deja's MCP server and writes an extension of its own. When that
extension is present this package stands down, so having both never injects
twice.

omp loads `pi.extensions` manifests and emits the same `before_agent_start`
event, so `pi install`'s omp counterpart picks this package up as well.

## What it does

- **Session start**: a digest of what earlier sessions in this project decided
  goes to the model before your first prompt; the receipt shows in the footer.
- **Every prompt** (`before_agent_start`): the prompt is matched against the
  index and, when a past session answers it, that session goes to the model
  with the prompt. Silence is the common case.
- **`/deja <query>`**: search the history by hand.

## Which binary

In order: `DEJA_BIN`, `deja` on `PATH`, the usual install locations, and last
the copy bundled through `@vshulcz/deja-vu`.

## Privacy

deja reads session files where the agents wrote them and builds a local index
under `~/.cache/deja` (or `DEJA_INDEX_DIR`). Keys and tokens are stripped as
the index is built. Nothing is uploaded.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
