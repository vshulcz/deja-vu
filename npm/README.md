<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><strong>Your coding agents stop re-debugging what you already fixed.</strong></p>

**deja starts full**: the history 41 agents already wrote, with no model and no capture step. It
indexes the sessions your coding agents already wrote to disk — months of
history from before you installed it — and serves them back over MCP, in
whichever agent asks.

Forty-one coding agents write every conversation to local files, among them
Claude Code, Codex, Cursor, opencode, Gemini CLI, Cline, Copilot CLI, VS Code
Copilot Chat, Roo Code, Kilo Code, aider, Goose, Qwen Code, Kimi Code,
Antigravity, Grok Build, OpenClaw, pi, omp, DeepSeek Harness, Hermes, Kiro,
Reasonix and Zed. deja turns those files into one memory layer that all of them
can read.

One Go binary. No LLM, no embeddings, no API key. The network is used only by commands you run for it, such as `deja update` and `deja sync ssh`, and by a daily release check, which DEJA_OFFLINE=1 turns off.
**58% fewer tokens** on a task this machine had already solved (53,558 against 126,222 with nothing wired, and 52,815 against 103,443 on a later build with the arms run alternately, eleven runs an arm each time). **88.1% hit@1** on LongMemEval-S (470-question cleaned set), **millisecond** lookups over gigabytes of history.

```sh
npx @vshulcz/deja-vu "connection pool exhausted"   # search, no install
npm install -g @vshulcz/deja-vu                    # then: deja install --auto
```

`deja install --auto` wires MCP recall into every coding agent it finds on the
machine and turns on session-start recall where the agent supports it. Ten
seconds to install, about ten to index, and the next session already knows.
Use `--all` for the MCP tools without the session-start hook. `deja` on its own
opens a search screen over every agent's history, where you can read a session,
resume it, or continue it in another agent.

Full documentation, the harness matrix and the benchmarks:
[github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu) ·
[vshulcz.github.io/deja-vu](https://vshulcz.github.io/deja-vu/)

MIT

Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
