# deja for Claude Code

The plugin bundle Claude Code, Cursor, Qwen, OpenClaw and Copilot install from
this repository's marketplace:

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

It carries deja's MCP server, the `deja-history` skill, the `/deja` command and
the session-start, pre-compact, pre-tool and post-failure hooks — the same
wiring `deja install --auto` writes, packaged the way those harnesses install
things.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
twenty coding agents already write to disk, including sessions from before it
was installed, and answers from them locally: BM25 over the transcripts, no
model and no embeddings, credentials redacted as the index is built.

The binary is not in the bundle — plugins carry files, not native binaries:

```sh
brew install deja-vu
```

or

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Having both this bundle and `deja install` is fine: the hooks here stand down
when the installer has already wired the same ones, so nothing is recalled
twice.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](../SECURITY.md).

MIT, same as deja.
