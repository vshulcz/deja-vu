<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><strong>Your agent is about to re-debug something you fixed in March.</strong></p>

Every memory tool starts empty and records forward. **deja starts full.** It
indexes the sessions your coding agents already wrote to disk — months of
history from before you installed it — and serves them back over MCP.

Nineteen coding agents write every conversation to local files: Claude Code,
Codex, Cursor, opencode, Gemini CLI, Cline, Copilot CLI, Roo Code, aider,
Goose, Qwen Code, Kimi Code, Antigravity, Grok Build, OpenClaw, pi, Hermes and
Zed.
deja turns those files into one memory layer that all of them can read.

One Go binary. No LLM, no embeddings, no API key, nothing leaves the machine.
**84.9% hit@1** on LongMemEval-S, **~1.5 ms** median search over 3.5 GB.

```sh
npx @vshulcz/deja-vu "connection pool exhausted"   # search, no install
npm install -g @vshulcz/deja-vu                    # then: deja install --auto
```

`deja install --auto` wires MCP recall into every coding agent it finds on the
machine and turns on session-start recall where the agent supports it. Ten
seconds to install, about ten to index, and the next session already knows.
Use `--all` for the MCP tools without the session-start hook.

Full documentation, the harness matrix and the benchmarks:
[github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu) ·
[vshulcz.github.io/deja-vu](https://vshulcz.github.io/deja-vu/)

MIT
