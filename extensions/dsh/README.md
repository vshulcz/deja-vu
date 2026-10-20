# dsh-deja

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/dsh/docs/zh.md)

Part of [deja-vu](https://github.com/vshulcz/deja-vu). Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).

DeepSeek Harness can already search its own sessions — that is what the built-in
`session-query` subsystem does. This plugin answers the other question: what you
did in the *other* agents on this machine.

deja indexes the session files that forty-two other coding agents — Claude Code,
Codex, Cursor, opencode, Antigravity, Grok Build, Kimi, Cline and Zed among
them — already write to disk. Nothing has to have been recorded ahead of time: the history is already
there, including the months before deja was installed. If you moved to dsh last
week, everything you did before last week is still reachable from inside it.

## Install

```sh
dsh plugin --profile web add dsh-deja
```

From the repository instead of npm — the root manifest re-exports this
directory, so either form works:

```sh
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
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

And, unless you turn it off, automatic recall: once per session the plugin adds
what past sessions settled in this project, and before each step it asks deja
whether this machine's history answers the prompt and adds the answer to the
runtime context. Silence is the common case — it speaks only when there is
something to say.

The same recall also wires what `deja install dsh-auto` wires: after a read,
edit or write the model gets what past sessions said about that file, after a
command that failed it gets what this machine ran after the same error before,
a compaction keeps what the session was in the middle of, and a session that
ends drops its live stamp.

In `dsh web` you also see the lines deja prints for the person in Claude Code:
the "you have been here" receipt when a prompt was answered from history, and
at session start what was recalled, the first-build note and the weekly note.
They sit in a small panel above the input until the next prompt, or until you
close it. The model gets exactly what it got before; these lines never reach
it. tui, headless and sdk profiles have no page, so nothing changes there:
the notes still come with the reply to `/deja`. A line that waits more than
ten minutes for the page is dropped. `deja install dsh-auto` writes the same
panel from these files; with both installed you get one panel.

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

The person's lines take one deja call with the recall, not two: where dsh
serves the page, the plugin asks for the hook's JSON reply, hands `additionalContext` to the model and keeps
`systemMessage` on the host until the page asks for it at `/api/deja-notes`,
behind dsh's own login. Anywhere else it keeps asking for `--plain`, because
deja marks a note shown when it prints it. The browser half is `client.js`,
plain JS in dsh's module wrapper, so the package has no build step.

## What it does not do

No LLM, no embeddings. The index is a local BM25 store over files
that already exist, so a query answers in about a millisecond, with no network
path unless you ask for one. Secrets are redacted at index time.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT.
