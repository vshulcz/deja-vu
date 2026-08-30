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

For the twenty-one harnesses deja installs into — Claude Code, Codex, Cursor, opencode,
Gemini CLI, Cline, Copilot CLI, Roo Code, aider, Goose, Qwen Code, Kimi Code,
Antigravity, Grok Build, OpenClaw, pi, omp, DeepSeek Harness, Zed and Hermes —
there is a one-command setup instead:

```sh
deja install --auto   # MCP recall everywhere it finds, plus session-start recall
deja install --all    # the same without the session-start hook
```

## Verify

```sh
deja warmup           # builds the local index (~10s for a few GB of history)
deja "test query"     # CLI search works
```

The MCP tools:

- `recall` — dense results under ~4KB for a query.
- `recall_context` — markdown digest of the best-matching session.
- `blame` — which sessions discussed a file, and what was decided.
- `fix` — what this machine ran after the same error last time.
- `how` — the real invocation for a tool here, with its real flags.
- `remember` — store one durable decision for a later session to recall.

No API keys, no network access, no configuration required.
