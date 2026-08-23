# deja for Kimi Code

[deja](https://github.com/vshulcz/deja-vu) indexes the session files coding
agents already write to disk — Claude Code, Codex, Cursor, opencode and sixteen
more — and answers from them. This plugin brings that index into Kimi Code:
recall arrives with the prompt, and the agent can search history itself.

## Install

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

That form pins the latest release and records where it came from, which is what
Kimi's update check reads. There is also a `kimi-deja.zip` release asset — 16 KB
instead of a 3 MB repository — for a marketplace entry or an install that should
not pull the whole project.

## Updates

Kimi only notifies about updates for plugins installed from its own
marketplace. For a repository install, `/plugins install` again pulls the
current release, and `deja doctor` says when the copy you have is behind the
one this deja ships:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.1.0 installed, v0.2.0 ships with this deja — reinstall it in Kimi to update)
```

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
