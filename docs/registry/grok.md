# Grok Build session format

## Store and files

Grok Build stores sessions below `${GROK_HOME:-~/.grok}/sessions/<encoded-cwd>/<session-id>/`. `DEJA_GROK_ROOT` overrides the read root. `updates.jsonl` is the conversation stream and sibling `summary.json` carries metadata. A `.cwd` file beside session directories can recover the working directory when summary metadata is absent.

The working-directory group is URL-encoded, although observed names are not always encoded consistently. deja prefers `summary.json` and `.cwd` over decoding the directory name.

## Records

`summary.json` includes `info.id`, `info.cwd`, titles, and RFC 3339 creation/update times. Conversation lines use ACP session updates:

```json
{"timestamp":1784278802,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The first chunk "}},"_meta":{"promptId":"prompt-1"}}}
```

`user_message_chunk` maps to `user` and `agent_message_chunk` maps to `assistant`. Content is usually `{ "type": "text", "text": "..." }`; arrays of text-bearing parts are also accepted. Timestamps accept Unix seconds or milliseconds. `_meta.agentTimestampMs` is the fallback.

Consecutive assistant chunks with the same `promptId` are joined. Consecutive user chunks with the same `promptIndex` are joined.

## Known quirks and drift

- The ACP stream contains large tool updates. deja filters lines for message chunk kinds before decoding JSON.
- Rewind can truncate and regrow `updates.jsonl`; changed streams are reparsed in full.
- `generated_title` takes precedence over `session_summary`.
- Missing summary files fall back to directory IDs and the `.cwd` or URL-decoded path.
- Path encoding is ambiguous when upstream leaves separators or percent escapes in different forms.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
