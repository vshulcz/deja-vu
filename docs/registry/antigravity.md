# Antigravity session format

## Store and files

Antigravity stores transcripts at `~/.gemini/antigravity*/brain/<session-id>/.system_generated/logs/transcript.jsonl`. The wildcard reflects observed versioned or profile-specific roots. `DEJA_ANTIGRAVITY_ROOT` replaces root discovery.

## Records

Each line has a source, content, and creation time:

```json
{"source":"USER_EXPLICIT","created_at":"2026-07-17T09:00:00Z","content":"<USER_REQUEST>Check the build.<ADDITIONAL_METADATA>{\"cwd\":\"/work/api\"}</ADDITIONAL_METADATA></USER_REQUEST>"}
```

`USER_EXPLICIT` maps to `user`; other sources except `MODEL` are ignored. A `MODEL` line is split by its `type`: `PLANNER_RESPONSE` (or no type) is assistant text, `RUN_COMMAND` and `GENERIC` give a command record from their `Task Description:` line, and `VIEW_FILE`, `CODE_ACTION` and `LIST_DIRECTORY` give a file record for the path they name. A planner row's `tool_calls` are read too: `run_command` gives a command record from `CommandLine` (its `Cwd` names the project when nothing else does), and `view_file`, `replace_file_content` and `write_to_file` a file record from `AbsolutePath` or `TargetFile`. On disk each arg is itself JSON, the quotes included, and is decoded once; a step header naming the same command or file again is not counted twice. The body of a tool step, minus its `Created At:`/`Completed At:` header, is kept as tool output. A `GENERIC` or `RUN_COMMAND` step whose header says "The command exited with code N." puts `→ exit N` on the command it ran: the one the step names, else the only `run_command` of the planner row before it; a step carries no call id, so with two calls and no name the code is left off (#4530). `created_at` is RFC 3339. The directory below `brain` is the session ID. A `write_to_file` step says only "Created file", with no diff block, so the call's `CodeContent` is the written side, recorded once a `CODE_ACTION` step with status `DONE` names the file; a failed step names none and nothing is recorded (#4528).

The transcript carries no workspace. deja takes the project from the IDE's `cache/conversation_metadata.json` (`WorkspaceURIs`), then from the CLI's `cache/last_conversations.json`, and failing both from the deepest directory shared by the absolute paths the session touched; otherwise it records `-`.

Before indexing user text, deja removes the outer `<USER_REQUEST>` wrapper, complete `<ADDITIONAL_METADATA>` and `<USER_SETTINGS_CHANGE>` blocks, and the `Comments on artifact URI:` lines the IDE adds when a plan is approved or rejected. Content is capped at 1 MiB per message.

A tool step names its file in a sentence ("The following changes were made by
the replace_file_content tool to: `<path>`"), not on a labelled line, and
carries the change as a `[diff_block_start]` … `[diff_block_end]` block. Both
sides of that block are read: the removed lines are the replaced span, the added
lines are the written side `deja blame` attributes a line by. Some blocks carry
unified `--- a/x` / `+++ b/x` headers, which are not lines of the file and are
skipped.

## Handoff

Antigravity's terminal client is `agy`, and `agy -i <prompt>` opens an
interactive session on it, so `deja handoff --to agy` — or `--to antigravity`,
the same target — starts it directly. Verified on agy 1.1.7. On the screen
bare `deja` opens, `o` on any session hands it to Antigravity the same way, and
`r` on an Antigravity session runs `agy --conversation <id>`.

## Hooks

`PreInvocation` is the one hook whose answer reaches the model, so it carries
what other hosts split across events. After the first invocation it reads the
transcript the payload names: a planner step that called
`replace_file_content`, `multi_replace_file_content` or `write_to_file` gets
the line deja keeps for that file, and a `CHECKPOINT` step, which is how a
compaction shows, gets the session as it stood before it, once. Antigravity
keeps the steps before a checkpoint, so nothing else is needed.

Antigravity has no session-end event. `PreInvocation` marks the conversation
live, and a `Stop` hook, fired when the execution loop ends, runs
`deja hook-session-end` so another conversation's MCP recall can answer with
it between turns.

## Status line

agy runs a command for its status line, set by `/statusline <command>` or the
`statusLine` key in `~/.gemini/antigravity-cli/settings.json` (agy 1.3.1).
`antigravity-auto` writes `deja statusline` there with `stack_with_default`,
so agy's own line stays above deja's. agy pipes `session_id` and a
`transcript_path` that ends in `transcript.jsonl` for every conversation, so
the line finds the session by its id. A status line already set up there is
left alone.

## Known quirks and drift

- User-visible content and machine metadata share one string field.
- System events and other source values can be interleaved with conversation records.
- A malformed or partially written JSONL line is skipped.
- If a timestamp is absent, later messages fall back to the session start when one has already been observed.

**Last verified:** 2026-09-20
