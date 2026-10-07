# deja-memory for Hermes

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/hermes/docs/zh.md)

A Hermes memory provider that answers from the coding sessions already on this
machine: Claude Code, Codex, Cursor, Hermes itself and the other agents deja
reads, including the months before anything was installed. No model, no API
key, no server. The history is indexed locally by
[deja](https://github.com/vshulcz/deja-vu), a single Go binary.

## Install

deja has to be on PATH first:

```bash
brew install deja-vu        # or: go install github.com/vshulcz/deja-vu/cmd/deja@latest
hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

or pick `deja-memory` in `hermes memory setup`, which checks for the binary
before it activates the provider.

## What it does

- Before the first turn of a session it hands Hermes the digest deja's hooks
  give Claude Code and Codex: the recent work in this project and what held.
  After that, only when what was just asked matches something earlier. Most
  turns it adds nothing.
- Three tools: `deja_recall` searches past sessions, `deja_fix` says what was
  run after an error last time, `deja_blame` lists the sessions that edited a
  file.
- An entry Hermes adds to MEMORY.md or USER.md is kept as a deja note too, so
  it ranks above the sessions around it.
- A compaction keeps what the session was in the middle of for the next turn,
  and the end of a session drops its live stamp.

One thing a memory provider cannot do: put the earlier fix beside a command
that just failed. That rides `transform_tool_result`, a plugin hook, and Hermes
hands a memory provider a context whose `register_hook` does nothing
(`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` writes the
hook plugin that carries it.

It never writes Hermes turns anywhere: Hermes already stores them, and deja
reads that store on its next refresh.

## Same code as `deja install`

`deja install hermes-auto` writes this provider to `~/.hermes/plugins/deja-memory`
along with the MCP server and a pre_llm_call hook plugin. The files here are
generated from the same source (`cmd/deja/install_hermes_memory.go`); the only
difference is that this copy finds deja on PATH instead of having the path
written in. `TestHermesExtensionIsTheProviderInstallWrites` fails when the two
drift.
