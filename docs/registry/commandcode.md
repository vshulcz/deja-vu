# Command Code

- **ID**: `commandcode`
- **Store**: `~/.commandcode/projects/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_COMMANDCODE_ROOT` replaces the project root
- **Format**: flat JSONL — one message per line
- **Needs**: nothing

Command Code writes a Claude-Code-style project directory with a plain
transcript in it: each line is one message, carrying `role`, `content`,
`timestamp` and `sessionId`, with no envelope around it. `content` is either a
string or the block array Claude's format uses, and both are read.

**Last verified:** 2026-09-16

## Known quirks and drift

- **A sibling with the same extension.** `<session>.checkpoints.jsonl` sits
  beside the transcript and is a snapshot stream, not a conversation — read as
  one it adds a session with no words in it that then competes for a recall
  slot. It is skipped by name, and a test pins that.
- A line whose role is neither `user` nor `assistant` is tool output, which is
  where a command's error text lives. It is indexed as tool output rather than
  dropped, so a user can search for an error they have already hit.
- Read support only.
