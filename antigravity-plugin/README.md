# deja for Antigravity

The plugin bundle Antigravity and the `agy` CLI install from the Antigravity
marketplace or directly from this repository:

```sh
agy plugin install deja-vu
```

It carries deja's MCP server, the `deja-history` skill, always-on guidance in
`rules/AGENTS.md`, and the `PreInvocation` and `Stop` hooks — the same wiring
`deja install antigravity-auto` writes, packaged as a native Antigravity plugin.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
forty-three coding agents already write to disk, including sessions from before
it was installed, and answers from them locally: BM25 over the transcripts, no
model and no embeddings, credentials redacted as the index is built.

The binary is not in the bundle — plugins carry files, not native binaries:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Having both this bundle and `deja install antigravity-auto` is fine: the hooks
here stand down when the installer has already wired the same ones, so nothing
is recalled twice.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](../SECURITY.md).

MIT, same as deja.
