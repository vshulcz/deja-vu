# dsh-deja

English | [中文](README.zh.md)

DeepSeek Harness can already search its own sessions — that is what the built-in
`session-query` subsystem does. This plugin answers the other question: what you
did in the *other* agents on this machine.

deja indexes the session files that Claude Code, Codex, Cursor, opencode,
Antigravity, Grok Build, Kimi, Cline, Zed and ten more agents already write to
disk. Nothing has to have been recorded ahead of time: the history is already
there, including the months before deja was installed. If you moved to dsh last
week, everything you did before last week is still reachable from inside it.

## Install

```sh
dsh plugin --profile web add dsh-deja
```

The plugin runs the `deja` binary. It is pulled in as a dependency, so npm
installs it with the plugin; a `deja` already on `PATH` is used as-is, and
`DEJA_BIN` overrides both.

If you have the CLI, `deja install --auto` wires dsh too — it adds deja's MCP
server and a `/deja` command to your profile — and that is the shorter path.
Having both is fine: this plugin looks for what the installer wrote in
`$DSH_HOME/plugins/deja/` and contributes only what is missing, so the tools and
the command are never registered twice.

## What it adds

Six tools the model can call:

| Tool | Answers |
|---|---|
| `deja_recall` | Sessions matching an error string, function name, file path or flag. |
| `deja_session` | The full digest of one past session, when the reasoning behind it matters. |
| `deja_blame` | The sessions that discussed a file, before you edit or delete it. |
| `deja_fix` | What this machine ran after that same error before. |
| `deja_how` | The real invocation for a build, test or deploy, from what ran here. |
| `deja_remember` | Stores one settled decision for later recall. |

One command: `/deja <what to look for>`.

And, unless you turn it off, automatic recall: before each step the plugin asks
deja whether this machine's history answers the prompt, and adds the answer to
the runtime context. Silence is the common case — it speaks only when there is
something to say.

```yaml
- insert:
    - id: deja
      name: dsh-deja
      config:
        autoRecall: false   # tools and /deja only
```

## How it is wired

Recall arrives through `ctx.systemPrompt.context`, evaluated on every assembly.
The obvious alternative — a middleware on `agent/pre-step` that splices a
message into the step — loads fine, completes the turn, and never reaches the
model: a later listener in that waterfall rebuilds its answer from the payload
and the added message is dropped with nothing reported. This was settled by
reading the requests dsh 0.1.1-rc.2 actually sent.

## What it does not do

No LLM, no embeddings, no network. The index is a local BM25 store over files
that already exist, so a query answers in about a millisecond and nothing leaves
the machine. Secrets are redacted at index time.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
