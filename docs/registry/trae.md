# TRAE CLI

- **ID**: `trae`
- **Store**: `${TRAE_HOME:-~/.trae}/cli/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`, plus `archived_sessions/` for `traex archive <id>` and `history.jsonl` beside them
- **Read override**: `DEJA_TRAE_ROOT` replaces the `cli` directory; `TRAECLI_HOME` moves that directory and `TRAE_HOME` moves TRAE's whole home, and deja follows both
- **Format**: JSONL — Codex rollouts: `session_meta`, `turn_context`, `response_item`, `event_msg`, plus TRAE's own `history_mutation`
- **Needs**: nothing (`zstd` only for a compressed rollout)
- **Resume**: `traex resume <id>`
- **Wiring**: `deja install trae` adds the MCP server to `${TRAE_HOME:-~/.trae}/traecli.toml`; `deja install trae-auto` adds Codex's hooks to `${TRAECLI_HOME:-${TRAE_HOME:-~/.trae}/cli}/hooks.json` as well, which TRAE runs once they are trusted at start-up

TRAE CLI 2.0 ships as `traex` and reports itself as `traecli 0.200.x`. It is a
closed-source fork of codex-rs and writes the same rollout files Codex does,
with `originator: "codex-tui"`, one level further down at `~/.trae/cli`. deja
reads them with the Codex reader and files the sessions under `trae`, not
`codex`, because they resume with `traex` and three things in them are read
differently.

**Who said it.** TRAE writes its own runtime injections — environment context,
process-limit notices — as `response_item` messages under the `user` role, so
that record is not evidence a person typed anything. A prompt is taken only
from `event_msg` `user_message` or, from 0.201.4, `event_msg` `item_completed`
carrying a `UserMessage` item. The two can describe the same prompt, in either
order, and the older one sometimes has no `turn_id`; a pair is read once — same
text and turn id, or same text within five seconds when one side has no id — and the same
words in a later turn are a second prompt.

**The answer.** It is taken from the first of these that the rollout has:
assistant `response_item` messages, assistant messages in `history_mutation`,
`item_completed` `AgentMessage` items (text blocks typed `Text`, `text` or
`output_text`), and `agent_message` events.

**Tool calls.** They ride in `history_mutation` records with `operation:
"append"`. The shell is `exec` with an argv under `command`
(`["bash","-lc","<script>"]`, the script is what is kept); a result's `output`
is a block list whose text blocks are joined; `apply_patch` is the Codex patch
format. A call seen both there and as a `response_item` is read once, by
`call_id`. A `replace` re-sends history the file already holds and is skipped.

**Last verified:** 2026-10-08

## Known quirks and drift

- **The shapes come from other readers, not from a running install.** The
  user-turn rule, the `item_completed` dialect and `history_mutation` are read
  from botmux's TRAE reader (`src/services/traex-transcript.ts` and its tests);
  the store layout and the resume command from agentsview's TraeX provider
  (`internal/parser/traex.go`, `types.go`, `server/resume.go`). Nothing here
  has been checked against a rollout TRAE wrote on this machine.
- **An append that splits a pair is read whole.** When the new bytes hold the
  second record of a prompt, or a repeat of a call, read before the offset,
  the incremental pass reads the file from the start, so neither lands twice.
- **The 1.x store is not read.** TRAE CLI 1.x (`traecli`, internally CoCo)
  wrote `events.jsonl` under `~/Library/Caches/coco/sessions/` on macOS and
  `~/.cache/coco/sessions/` on Linux, a different format.
- **The IDE store is not read, but the IDE is wired.** TRAE, TRAE CN and TRAE
  SOLO keep chat in a SQLCipher-encrypted `ModularData/ai-agent/database.db`.
  `deja install trae-ide` adds the server to `<user data>/User/mcp.json`
  (`~/Library/Application Support/Trae`, `%APPDATA%\Trae` or `~/.config/Trae`;
  `Trae CN` for the CN build) with no `type` key, which its schema rejects, and
  the skill to `~/.trae/skills` (`~/.trae-cn/skills`); with both builds installed,
  each is wired in its own files.
  `deja install trae-ide-auto` adds Claude-shaped hooks to `~/.trae/hooks.json`. The IDE ships
  with hooks off and keeps that switch in its own settings store, so deja cannot
  tell whether they run: turn them on in Settings > Hooks and run them locally.
- **The skill is the command.** TRAE CLI reads the deja-history skill from
  `~/.agents/skills`, and its system prompt (0.207.1) has the model run a skill
  when the user types `/<skill-name>`, so `/deja-history <query>` is deja's
  command. It has no custom-prompt directory of its own.
- **`~/.trae/hooks.json` is the IDE's, not the CLI's.** TRAE CLI 1.x read it;
  2.0 reads `cli/hooks.json` and says the old file is no longer read.
