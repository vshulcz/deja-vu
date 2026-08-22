# Grok Build session format

## Store and files

Grok Build stores sessions below `${GROK_HOME:-~/.grok}/sessions/<encoded-cwd>/<session-id>/`. `DEJA_GROK_ROOT` overrides where deja reads sessions; `GROK_HOME` relocates the whole Grok tree, including `config.toml`. `updates.jsonl` is the conversation stream and sibling `summary.json` carries metadata. A `.cwd` file beside session directories can recover the working directory when summary metadata is absent.

The working-directory group is URL-encoded, although observed names are not always encoded consistently. deja prefers `summary.json` and `.cwd` over decoding the directory name.

## Two products share this directory

`@vibe-kit/grok-cli` (npm) also keeps its configuration under `~/.grok`, and the
two are unrelated: it reads `~/.grok/user-settings.json` plus a project-level
`.grok/settings.json`, and never looks at `config.toml`. Its MCP servers are
**per project** — `grok mcp add` writes into the working directory — so a
global `deja install` cannot wire it. Run `grok mcp add deja --command deja
--args mcp` inside a project to use deja there.

## Records

`summary.json` includes `info.id`, `info.cwd`, titles, and RFC 3339 creation/update times. Conversation lines use ACP session updates:

```json
{"timestamp":1784278802,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The first chunk "}},"_meta":{"promptId":"prompt-1"}}}
```

`user_message_chunk` maps to `user` and `agent_message_chunk` maps to `assistant`. Content is usually `{ "type": "text", "text": "..." }`; arrays of text-bearing parts are also accepted. Timestamps accept Unix seconds or milliseconds. `_meta.agentTimestampMs` is the fallback.

Consecutive assistant chunks with the same `promptId` are joined. Consecutive user chunks with the same `promptIndex` are joined.

## Spawn tree

`summary.json` records what a session is and where it came from:
`session_kind` (`subagent`, `subagent_fork`), `parent_session_id`, `agent_name`
and `forked_at`. deja reads the first three into the session record — they show
up in `--json` as `kind`, `parent` and `agent`, and `deja show` names the
session a child was spawned from and the children a parent spawned. A
`subagent` with no `parent_session_id` keeps its kind and no edge: which
session asked for it is not written down, and deja does not guess.

## Known quirks and drift

- The ACP stream contains large tool updates. deja filters lines for message chunk kinds before decoding JSON.
- Rewind can truncate and regrow `updates.jsonl`, which looks like growth from
  the outside. deja compares the prefix hash it recorded: an intact prefix takes
  the append path and reads only the new bytes, a moved one reparses the stream
  in full. A live session used to rewrite the whole index on every touch.
- `generated_title` takes precedence over `session_summary`.
- Missing summary files fall back to directory IDs and the `.cwd` or URL-decoded path.
- Path encoding is ambiguous when upstream leaves separators or percent escapes in different forms.

**Last verified:** 2026-07-27
