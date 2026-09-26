# Installing deja-vu (for AI agents)

deja is a single zero-dependency binary. Pick one install path:

```sh
npm install -g @vshulcz/deja-vu     # or: npx -y @vshulcz/deja-vu
# or: brew install deja-vu
# or: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

## Wire the MCP server

The stdio server is `deja mcp`. For clients with a JSON MCP config (Cline,
Claude Code, etc.) add:

```json
{
  "mcpServers": {
    "deja": {
      "command": "deja",
      "args": ["mcp"]
    }
  }
}
```

If `deja` is not on PATH, use the npx form: `"command": "npx", "args": ["-y", "@vshulcz/deja-vu", "mcp"]`.

deja reads the session files of thirty-four coding agents and wires itself into
most of them — Claude Code, Codex, Cursor, opencode, Gemini CLI, Cline, Roo
Code, Kilo Code, Copilot CLI, aider, Goose, Qwen Code, Kimi Code, Antigravity,
Grok Build, OpenClaw, pi, omp, DeepSeek Harness, Zed, Hermes and the rest that
`deja install --help` lists. For those there is a one-command setup instead:

```sh
deja install --auto   # MCP recall everywhere it finds, plus session-start recall
deja install --all    # the same without the session-start hook
```

## Verify

```sh
deja warmup           # builds the local index (about a minute for a few GB; search
                      # answers from the newest sessions while the rest finishes)
deja "test query"     # CLI search works
```

One MCP tool, `deja`, with a `mode` argument (clients wired earlier can still call `recall`, `recall_context`, `blame`, `fix`, `how` and `remember` as tools of their own):

- `recall` — dense results under ~4KB for a query.
- `context` — markdown digest of the best-matching session (was `recall_context`).
- `blame` — which sessions discussed a file, and what was decided.
- `fix` — what this machine ran after the same error last time.
- `how` — the real invocation for a tool here, with its real flags.
- `orient` — the commands past sessions ran in this project and the files they worked in, before reading the tree.
- `remember` — store one durable decision for a later session to recall.

No API keys and no configuration. Indexing, search and every tool above are
local and make no network calls; the exceptions are commands somebody runs on
purpose — `deja update`, `deja doctor`'s version check, `deja sync ssh` and
`deja embed` against a model endpoint you name. SECURITY-MODEL.md lists them.
