# Installing deja-vu (for AI agents)

deja is a single zero-dependency binary. Pick one install path:

```sh
npm install -g @vshulcz/deja-vu     # or: npx -y @vshulcz/deja-vu
# or: brew install vshulcz/tap/deja-vu
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

For Claude Code, Codex and opencode there is a one-command setup instead:

```sh
deja install --all    # MCP everywhere; add --auto for session-start auto-recall
```

## Verify

```sh
deja warmup           # builds the local index (~10s for a few GB of history)
deja "test query"     # CLI search works
```

The MCP tools are `recall` (dense results under ~4KB) and `recall_context`
(markdown digest of the best-matching session). No API keys, no network
access, no configuration required.
