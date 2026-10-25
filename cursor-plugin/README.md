# deja for Cursor

The Cursor plugin for [deja](https://github.com/vshulcz/deja-vu). It carries
deja's MCP server, the `deja-history` skill, the `/deja` command and the
`sessionStart`, `beforeSubmitPrompt`, `preToolUse`, `postToolUse`,
`postToolUseFailure`, `preCompact` and `sessionEnd` hooks: the same wiring
`deja install cursor-auto` writes.

deja indexes the session transcripts forty-three coding agents already write to
disk, including sessions from before it was installed, and answers from them
locally: BM25 over the transcripts, no model and no embeddings, credentials
redacted as the index is built.

## Install deja first

The plugin does not carry the binary. Install it with Homebrew:

```sh
brew install deja-vu
```

or npm:

```sh
npm i -g @vshulcz/deja-vu
```

Until `deja` is on PATH (or in `~/.local/bin`, `/opt/homebrew/bin`,
`/usr/local/bin` or `~/go/bin`) the hooks do nothing and the MCP server reports
that it could not start.

The hooks and the server start through `sh`, so this plugin works on macOS and
Linux. On Windows use `deja install cursor-auto` instead.

Having both this plugin and `deja install cursor-auto` is fine: the hooks here
stand down when the installer has already wired the same ones in
`~/.cursor/hooks.json`.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](SECURITY.md).

MIT, same as deja.
