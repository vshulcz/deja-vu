<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><strong>Your agent is about to re-debug something you fixed in March — in a different agent.</strong></p>

**deja starts full**: the history 34 agents already wrote, with no model and no capture step. It
indexes the sessions all 34 of your coding agents already wrote to disk — months
of history from before you installed it — and serves them back over MCP, in
whichever agent asks.

Thirty-four coding agents write every conversation to local files: Claude Code,
Codex, Cursor, opencode, Gemini CLI, Cline, Copilot CLI, VS Code Copilot Chat, Roo Code, aider,
Goose, Qwen Code, Kimi Code, Antigravity, Grok Build, OpenClaw, pi, omp,
DeepSeek Harness, Hermes and Zed.
deja turns those files into one memory layer that all of them can read.

One Go binary. No LLM, no embeddings, no API key, and no network path unless you ask for one.
**88.1% hit@1** on LongMemEval-S (470-question cleaned set), **millisecond** lookups over gigabytes of history.

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

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
