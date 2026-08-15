<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><strong>Your agents already solved this. deja finds it.</strong></p>

Memory tools start empty and record forward. deja starts full. It indexes the
sessions your coding agents already wrote to disk, including months of history
from before you installed it, and serves them back to any agent over MCP.

One Go binary. No LLM, no embeddings, no API key, nothing leaves the machine.

```sh
npx @vshulcz/deja-vu "connection pool exhausted"   # search, no install
npm install -g @vshulcz/deja-vu                    # then: deja install --all
```

`deja install --all` wires MCP recall into every coding agent it finds on the
machine; `--auto` adds session-start recall where the agent supports it. After
that, ask your agent *"have we dealt with this before?"* — or don't, and it will
start each session already knowing.

Full documentation, the harness matrix and the benchmarks:
[github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu) ·
[vshulcz.github.io/deja-vu](https://vshulcz.github.io/deja-vu/)

MIT
