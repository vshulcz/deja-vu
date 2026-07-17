# Antigravity session format

## Store and files

Antigravity stores transcripts at `~/.gemini/antigravity*/brain/<session-id>/.system_generated/logs/transcript.jsonl`. The wildcard reflects observed versioned or profile-specific roots. `DEJA_ANTIGRAVITY_ROOT` replaces root discovery.

## Records

Each line has a source, content, and creation time:

```json
{"source":"USER_EXPLICIT","created_at":"2026-07-17T09:00:00Z","content":"<USER_REQUEST>Check the build.<ADDITIONAL_METADATA>{\"cwd\":\"/work/api\"}</ADDITIONAL_METADATA></USER_REQUEST>"}
```

`USER_EXPLICIT` maps to `user` and `MODEL` maps to `assistant`. Other sources are ignored. `created_at` is RFC 3339. The directory below `brain` is the session ID; the transcript does not currently provide project metadata, so deja records `-`.

Before indexing user text, deja removes the outer `<USER_REQUEST>` wrapper and complete `<ADDITIONAL_METADATA>` and `<USER_SETTINGS_CHANGE>` blocks. Assistant content is retained as written. Content is capped at 64 KiB.

## Known quirks and drift

- User-visible content and machine metadata share one string field.
- System events and other source values can be interleaved with conversation records.
- A malformed or partially written JSONL line is skipped.
- If a timestamp is absent, later messages fall back to the session start when one has already been observed.

**Last verified:** 2026-07-17
**deja parser version:** v0.12.0-1-g9e2088c
