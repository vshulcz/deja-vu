# deja-vu for Spec Kit

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/spec-kit/docs/zh.md)

A [Spec Kit](https://github.com/github/spec-kit) extension. Before you implement
a feature, `/speckit.deja-vu.recall` searches your past coding-agent sessions
for it and reports what was tried, what was decided and what failed, with the
session id behind each point.

The sessions come from [deja](https://github.com/vshulcz/deja-vu), which
indexes the transcripts Claude Code, Codex, Cursor, Copilot, Gemini and other
agents already write to disk. Everything stays on your machine.

## Install

Install deja first:

```sh
brew install deja-vu
# or
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Then, in a Spec Kit project:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Each deja release attaches `deja-spec-kit.zip`, at the same version as the
release. To try a local checkout instead:
`specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Use

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

With no arguments it reads the active feature's `spec.md` and `plan.md` and
picks the queries itself. With arguments it searches for those words. The
command name follows your agent's style, so it may show up as
`/speckit-deja-vu-recall` or `$speckit-deja-vu-recall`.

Under the hood it runs, from the repository root:

```sh
deja recall -- "<query>"   # matching sessions, this project first
deja files -- "<query>"    # files opened or edited on that topic
```

and `deja show <id>` when an excerpt is too short to tell what happened. The
report is a `Prior sessions` section split into tried and failed, decided,
files and open, each point cited as `(deja:<id>)`.

## Hook

The extension registers one optional `before_implement` hook. When you run
`/speckit.implement`, the agent offers to run the recall first; nothing runs
unless you say yes. To turn it off, set `enabled: false` on the entry in
`.specify/extensions.yml`, or `specify extension disable deja-vu`.

## Notes

- Read-only: the command searches and reads, and edits nothing.
- The first deja call on a machine builds the index, which can take a while on
  a large history. After that it answers in well under a second.
- Recalled session text is treated as reference data; the command tells the
  agent never to follow instructions found in it.

## License

MIT
