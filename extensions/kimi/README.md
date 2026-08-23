# deja for Kimi Code

[deja](https://github.com/vshulcz/deja-vu) indexes the session files coding
agents already write to disk — Claude Code, Codex, Cursor, opencode and sixteen
more — and answers from them. This plugin brings that index into Kimi Code:
recall arrives with the prompt, and the agent can search history itself.

## Install

```
/plugins install https://github.com/vshulcz/deja-vu/releases/latest/download/kimi-deja.zip
/reload
```

Installing from the repository URL does not work: Kimi looks for
`kimi.plugin.json` at the repository root or in a single top-level directory,
and this is a monorepo. From a clone, `/plugins install <path>/extensions/kimi`
does work.

Then install the binary if you do not have it:

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

Plugins load from the managed copy, so run `/reload` or `/new` after installing.

## What you get

- **Recall on every prompt.** A `UserPromptSubmit` hook runs `deja hook-prompt`
  and Kimi appends what it finds to the turn. Silent when nothing matches.
- **Tools.** The plugin declares `deja mcp` as an MCP server: `recall`,
  `recall_context`, `blame` and `remember`.
- **`/deja:recall <query>`** to search history directly.
- **The `deja-history` skill**, loaded at session start, so the agent knows to
  look before re-debugging something.

## Which binary

In order: `DEJA_BIN`, then the deja you installed yourself (`~/.local/bin`,
`/usr/local/bin`, `/opt/homebrew/bin`), then `deja` on `PATH`. Your own
`deja update` or `brew upgrade deja` wins over anything a plugin release
pinned.

## If you also ran `deja install kimi`

The CLI writes the same MCP server into `~/.kimi-code/mcp.json` and the same
hook into `config.toml`. Kimi runs a plugin's server under its own namespace
(`plugin-deja:deja`), so both would run: every tool listed twice, recall
appended twice.

This plugin checks for the installer's wiring and stands down when it finds it —
the CLI's copy is the one `deja install` keeps current. `deja uninstall kimi`
hands ownership back to the plugin, with no reinstall in between: the check runs
when the hook fires and when the server starts, not once at install time.

`deja doctor` reports `plugin` for kimi when the plugin is what carries the
wiring, so a plugin-only machine does not read as unwired.

Two things it cannot see: an MCP entry you added by hand under a name other than
`deja` (it would run beside this one), and a hook you wrote yourself that calls
deja without the installer's marker comment.

## License

MIT
