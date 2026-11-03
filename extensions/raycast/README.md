# deja-vu for Raycast

Search and resume coding agent sessions across Claude Code, Codex, Cursor, OpenCode, Gemini CLI and 38 more harnesses from Raycast.

## Prerequisites

Install [`deja-vu`](https://github.com/vshulcz/deja-vu):

```sh
brew install deja-vu && deja install --auto
```

## Commands

- **Search Sessions**: Full-text search across every coding agent session on your Mac (`deja search --json --limit 25 -- <query>`), separating strict matches from nearest-session relevance hits.
- **Recent Sessions**: Browse the 100 most recent sessions across all installed agents (`deja last 100 --json`) with metadata preview (`Agent`, `Project`, `Started`, `Updated`, `Session ID`).

## Actions

- **Show Session**: Read the redacted conversation inside Raycast (`deja show <id> --json --limit 200`).
- **Resume in Terminal**: Run the agent's native resume command (`deja resume <id>`) in `Terminal.app`.
- **Copy Resume Command**, **Copy Session ID**, and **Open Project Folder**.

## Local development

```sh
cd extensions/raycast && npm install && npx ray develop
```
