# deja for Grok Build

Grok remembers its own sessions. This plugin answers the other question: what
you did in Claude Code, Codex, Cursor, opencode, Zed and fifteen more agents on
this machine — including the months before you installed anything.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, nothing leaves the machine.

## Install

```sh
grok plugin marketplace add xai-org/plugin-marketplace
grok plugin install deja
```

Install the binary first — plugins deliver files, not runtimes or native
binaries:

```sh
brew install vshulcz/tap/deja-vu
```

or

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

`deja install --auto` reaches Grok too, and is the shorter path when you have
the CLI: it writes `[mcp_servers.deja]` into `~/.grok/config.toml` and the four
hooks into `~/.grok/hooks/deja.json`. Either path is enough on its own.

## What you get

The MCP server, with the tools deja serves everywhere: `recall`,
`recall_context`, `blame`, `fix`, `how` and `remember`.

The `deja-history` skill, so the agent knows the CLI contract when the tools are
not reachable.

`/deja:recall <query>` for the times you want to ask directly.

Recall nobody has to ask for: a `UserPromptSubmit` hook searches this machine's
history for what the prompt is actually about, and stays silent when nothing
matches.

## Having both is fine

`deja install grok` and this plugin wire the same two things, and Grok would run
both — every tool listed twice, the same recall read twice on every prompt. So
each half stands down when it finds the installer's copy:

- the MCP server exits with a line saying why when `~/.grok/config.toml` has
  `[mcp_servers.deja]`;
- the hook returns without output when `~/.grok/hooks/deja.json` has deja's
  hooks in it.

Whatever `deja install` wrote wins, because that is the copy it keeps current.
`deja uninstall grok` hands ownership back to the plugin.

## Which binary

In order: `DEJA_BIN`, then a `deja` you installed yourself (`~/.local/bin`,
`/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), then the bare name so `PATH`
still has a say. Your own `deja update` or `brew upgrade` wins over anything a
plugin release froze.

Without deja anywhere, the hook stays silent and the MCP server says what is
missing instead of pretending the history is empty.

## Unverified here

`${GROK_PLUGIN_ROOT}` is what Grok's own plugin guide documents for hook
commands, and it is used the same way in `.mcp.json` above. That second use is
not something this machine could check against a running Grok Build — the `grok`
CLI installed here is the community project that shares the name and the
`~/.grok` directory, not xAI's Grok Build. If the variable is not expanded for
MCP servers, the server fails to start and the CLI-wired path still works; say
so in an issue and it will be fixed rather than guessed at again.

## License

MIT
