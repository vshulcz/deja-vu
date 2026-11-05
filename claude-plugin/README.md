# deja for Claude Code

The plugin bundle Claude Code, Qwen and OpenClaw install from
this repository's marketplace. Cursor has its own, in
[`cursor-plugin/`](../cursor-plugin/). Copilot CLI installs it too but takes only the
skill (no hooks, no MCP server); `deja install copilot-auto` wires those:

```sh
claude plugin marketplace add vshulcz/deja-vu && claude plugin install deja-vu@deja-vu
```

It carries deja's MCP server, the `deja-history` skill, the `/deja` command and
the session-start, per-prompt (`UserPromptSubmit`), pre-compact (`PreCompact`), pre-tool (`PreToolUse`), post-failure (`PostToolUseFailure`) and session-end (`SessionEnd`) hooks — the same
wiring `deja install --auto` writes, packaged the way those harnesses install
things.

`deja` indexes the session transcripts of every coding agent on the machine (`forty-three coding agents` total), across projects, and the hooks inject matching snippets from those local transcripts into the model's context (`UserPromptSubmit` recalls past sessions matching the prompt; `PreToolUse` surfaces decisions, verification commands, and co-changed files for edited paths, or pre-flight failure warnings before shell commands). Recalled transcripts are an untrusted text channel (they can contain earlier tool outputs or external content), so hook output is wrapped with a `<deja-recall>` untrusted-data framing banner and credentials are redacted at index time.

[deja](https://github.com/vshulcz/deja-vu) indexes the session transcripts
forty-three coding agents already write to disk, including sessions from before it
was installed, and answers from them locally: BM25 over the transcripts, no
model and no embeddings, credentials redacted as the index is built.

The binary is not in the bundle — plugins carry files, not native binaries:

```sh
brew install deja-vu
# or: npm i -g @vshulcz/deja-vu
```

Having both this bundle and `deja install` is fine: the hooks here stand down
when the installer has already wired the same ones, so nothing is recalled
twice.

Details, the harness matrix and the benchmarks are in the
[repository README](../README.md). Security policy: [SECURITY.md](../SECURITY.md).

MIT, same as deja.
