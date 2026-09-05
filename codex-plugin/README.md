# deja for Codex

The plugin bundle Codex installs from this repository's marketplace:

```sh
codex plugin marketplace add vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
```

It carries deja's MCP server and the `deja-history` skill.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
twenty-three coding agents already write to disk, including sessions from before it
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

`deja install codex` wires the same MCP server plus the session-start hook
Codex reads from `hooks.json`, which the bundle cannot carry. Either path
works, and having both does not wire anything twice.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](../SECURITY.md).

MIT, same as deja.
