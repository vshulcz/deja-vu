# aider session format

## Store and files

aider appends Markdown to `.aider.chat.history.md` at the git root it runs in (the launch directory outside a repo), so `~/.aider.chat.history.md` exists only for a launch from home; `AIDER_CHAT_HISTORY_FILE` relocates it. deja reads the home file, `AIDER_CHAT_HISTORY_FILE`, every project `deja aider` has started aider in (listed in `~/.config/deja/aider-projects`), and each directory in the platform path-list variable `DEJA_AIDER_ROOTS`, to two levels below each root.

One history file contains multiple sessions. A session begins with:

```markdown
# aider chat started at 2026-07-17 09:00:00

#### Show me the failing query

The query misses the tenant predicate.

> Applied edit to query.go
```

## Message mapping

Outside fenced code blocks, `#### ` starts or continues a user message. Plain Markdown is assistant output. Lines beginning with `> ` are tool or system output and are not indexed as messages; neither are unprefixed lines directly under one (the rest of that output block), nor anything before the session's first `#### ` line (the banner and the `--verbose` dump). Of the output, `Applied edit to X`, `Added X to the chat` and `Added X to read-only files` become a `files` record for X (relative to the history's directory), and `Running X` a `command` record. A `#### ` line that is one of aider's own commands (`/add`, `/undo`, `/clear`, …) is dropped; `/run X`, `!X` and `/test X` become a `command` record for X and `/git X` one for `git X`. `/ask`, `/code` and `/context` are dropped too: aider logs their question again as its own `#### ` line. Blank lines remain part of the current message.

The edit itself is in the reply, written as the model sent it. In the `diff` and `diff-fenced` formats it is a `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` block under the file's name: the SEARCH side becomes an `edit` record (the replaced span) and the REPLACE side a `wrote` record. `udiff` writes a unified diff in a ```` ```diff ```` fence and `patch` a `*** Begin Patch` block; their removed and added lines are read the same way. `whole` writes the new file in a fence under its name, which is a `wrote` record and no span; the format comes from the banner's `Model: … with <format> edit format` line (`Editor model:` in architect mode). A reply's edits are kept only for the files a following `Applied edit to X` line names, so a block that failed to match, which aider answers with `The LLM did not conform to the edit format`, records nothing. Checked against histories aider 0.86.2 wrote in each of `diff`, `udiff` and `whole` (#595).

The header timestamp uses local time with layout `YYYY-MM-DD HH:MM:SS`. aider does not store message timestamps, so every message receives the session start. It does not store a session ID; deja derives a stable ID from the history path and the session's start time (`aider-<path hash>-<YYYYMMDDTHHMMSS>`, with `-2` and on for a second launch in the same second, and the ordinal in the file when the header does not parse). The ordinal alone was the ID until #4332: a new history at the path of a deleted one restarted it and overwrote the sessions deja kept.

## Skills

None, and there is nothing to build on. aider loads instruction files rather
than offering a catalogue an agent picks from: `--read` (and a CONVENTIONS.md by
habit) puts a file in every message, which is exactly what deja's context file
already is. A skill here would be the same always-on text under another name.

## Known quirks and drift

- The file is append-only and can contain many launches.
- Markdown fences may contain lines beginning with `#### ` or `>`; these are content, not role markers. The parser tracks triple-backtick fences.
- `<blank>` after the user prefix represents an empty line.
- Tool output terminates an assistant block but does not become a message.
- Moving a history file changes deja's derived session IDs because the path is part of the ID.
- aider loads none of the history back into a new chat unless started with `--restore-chat-history`, and that loads the whole file, every session in it. `deja resume` refuses the session and names that flag in its message rather than claiming to reopen one session.
- A session that leaves the file — the file was deleted and aider started a new one, or someone cut it by hand — stays in the index, as a deleted transcript does; `deja forget --session <id>` drops it.

**Last verified:** 2026-07-27
