# deja for Grok Build

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/grok/docs/zh.md)

Grok remembers its own sessions. This plugin answers the other question: what
you did in Claude Code, Codex, Cursor, opencode, Zed and twenty-nine more agents on
this machine — including the months before you installed anything.

It runs [deja](https://github.com/vshulcz/deja-vu), a local Go binary that
indexes the transcripts those agents already wrote to disk. No LLM, no
embeddings, no network path unless you ask for one.

## Install

```sh
grok plugin marketplace add xai-org/plugin-marketplace
grok plugin install deja
```

Install the binary first — plugins deliver files, not runtimes or native
binaries:

```sh
brew install deja-vu
```

or

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

`deja install --auto` reaches Grok too, and is the shorter path when you have
the CLI: it writes `[mcp_servers.deja]` into `~/.grok/config.toml` and the four
hooks into `~/.grok/hooks/deja.json`. Either path is enough on its own.

## What you get

The MCP server, with the one tool deja serves everywhere: `deja`, called with
a mode of `recall`, `context`, `blame`, `fix`, `how`, `orient` or `remember`.

The `deja-history` skill, so the agent knows the CLI contract when the tools are
not reachable.

`/deja:recall <query>` for the times you want to ask directly.

A `UserPromptSubmit` hook that searches this machine's history for what the
prompt is about. Grok Build 1.0.5 discards what a prompt or tool hook prints, so
today that recall does not reach the model; the MCP tool is how it does. The one hook answer Grok does apply is a `PreToolUse` `updatedInput`, and
`deja install grok` uses it to put memory into a spawned agent's prompt.

## Having both is fine

`deja install grok` and this plugin wire the same two things, and Grok would run
both — every tool listed twice, the same recall read twice on every prompt. So
each half stands down when it finds the installer's copy:

- the MCP server answers the handshake and lists no tools when
  `~/.grok/config.toml` has `[mcp_servers.deja]`;
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

## Standing down without looking broken

`${GROK_PLUGIN_ROOT}` expands in `.mcp.json` as well as in hook commands —
measured on Grok Build 1.0.5 by launching a plugin server that wrote its own
argument back to a file, since neither `grok mcp doctor` nor `grok inspect`
reports plugin MCP servers.

When `deja install grok` already wired the CLI copy, this one answers the
handshake and lists no tools. Exiting instead would be reported as
`handshake failed: connection closed` and sit in the plugin UI as a broken
server, which is a worse thing to leave behind than an idle one.

## License

MIT
