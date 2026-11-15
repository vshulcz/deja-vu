# deja-vu for Orca

English | [简体中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/orca/docs/zh.md)

Orca v2 `trusted-worker` plugin for `deja` — brings session memory across forty-seven AI coding tools (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca, and more), including sessions recorded before `deja` was installed.

## Install

Requirements: [`deja`](https://github.com/vshulcz/deja-vu#install) on `PATH` (or installed under `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin`, or `%LOCALAPPDATA%\Programs\deja`).

Install the plugin directory `extensions/orca` in Orca (`>=1.4.248`).

For native MCP, skill files, `SessionStart`, `PreCompact`, `SessionEnd`, and failed-command fix capture inside Orca, also run:

```sh
deja install orca-auto
```

(`deja install orca` installs MCP, skill, and `/deja` without lifecycle hooks; `deja install orca-auto` or `deja install --auto` adds the full hook set.)

When `deja install orca-auto` has already written the native `UserPromptSubmit` hook into Orca's `settings.json`, this worker stands down automatically so `<deja-recall>` frames are not injected twice.

## What this plugin contributes

- **Hooks**
  - `agent.prompt.before`: resolves the active workspace path and injects relevant `<deja-recall>` context into the prompt within Orca's 2s hook budget (`1.3s` timeout, fail-open).
  - `agent.tool.before`: when spawning `Agent` or `Task` subagents, appends `<deja-recall>` context to `input.prompt` only (never modifies `Bash`, `Read`, `Edit`, or other tool inputs).
  - `agent.tool.after`: observe-only in Orca's host API (`next` is ignored and Orca does not wait), so failed-command `fix` delivery comes from `deja install orca-auto`.
- **Commands**
  - `deja.orient` (`deja: Orient on workspace`): runs `deja hook-context --plain` for the active workspace and shows a summary notification.
  - `deja.recall` (`deja: Recall past sessions`): runs `deja search --limit 5 -- <query>` (or `deja last -n 5` when no query is supplied) and shows the result.

## License

[MIT](LICENSE)
