# Automatic compaction recovery

With auto-recall installed, `hook-precompact` reads the current session when
the host compacts it. Claude Code, Codex and CodeBuddy transcripts go
through a bounded tail reader; every other host's session is found through the
source registry, by the transcript file the hook names or by the session id in
that harness's store, and read by the parser that indexes it. Hermes hands over
the turns themselves, and Gemini CLI is read after the fact, see below. It
extracts the user's
objective, assistant conclusions, recorded verification commands, and what a turn
says is still open or in conflict. An open item is recognised by its shape — the
line opens with the label, in either language ("Gap: …", "Осталось: …", "Still
open: …") — so a sentence that merely contains the word is not mistaken for one.
A turn that only tells the agent to carry on ("continue", "продолжай") is skipped
when looking for the objective, but a turn that carries an instruction is the
objective even when it opens with one of those words. Each item identifies its
source session, harness, role, and recorded timestamp. No checkpoint command or
model call is required.

The packet also carries a "Keep until closed" list: questions still waiting on
the user, open `#N` items, settled verdicts, and checks put off for later — the
lines a host summary tends to drop. Each capture starts from the list the last
one left, drops a line once the transcript closes it (for an open item, a merge
or close command or the user saying so), and ages out a line untouched for two
compactions. It is capped at twelve lines: three awaiting, four verdicts, three
rechecks, the rest open items. It is printed first and may take at most 40% of
the packet.

After it come the user's standing instructions: sentences from any user turn in
the session that tell the agent how to work ("Never modify internal/legacy/",
"не трогай legacy", "use slices instead of sort"), and a whole numbered list when
two thirds of its items are such instructions. Quoted lines, code fences,
harness blocks, questions and turns longer than 6 KiB are skipped. At most eight
lines and 1000 bytes, carried from one compaction to the next so a transcript
read from its tail does not lose them. When the newest request is a step
("Next: T13"), the first request of the session is printed beside it, labelled.

The next session-start, prompt, or tool hook for that same session and workspace
returns a recovery packet once. Its 4 KiB limit includes the untrusted-history
frame. The packet labels assistant conclusions as reported claims. A recorded
verification command is marked passed or failed from the harness's own exit
status when the transcript carries one, and otherwise from the output recorded
after it, by the same rule `deja friction` uses to decide whether a line is a
failure; a command whose output was not recorded stays unknown. Repository HEAD,
branch, and working-tree fingerprints are compared at recovery, and the packet
states the verdict — unchanged, or changed with the commit it was captured at —
rather than the fingerprints themselves. Changed, partial, or unavailable
fingerprints require validation instead of presenting old conclusions as
current.

## Storage and limits

Packets live in the existing `manifest.gob`, alongside index metadata. There is
no separate context store, additional MCP tool, or synthetic searchable session.
The index keeps the latest packet for up to 32 session/workspace pairs, with a
24 KiB limit per stored packet. A new capture replaces that session's packet;
the oldest captures are evicted when the session limit is reached. Only the
latest packet is kept; its keep-until-closed list is the one thing a new capture
reads from the old. Full and incremental index rebuilds preserve retained packets.
Packets are not included in sync exports.

The reader inspects at most 4 MiB of transcript tail plus a 64 KiB header. The
extractor selects at most 64 useful records and 32 KiB of text, with at most
eight items in each category. Omitted content is identified in the recovery
packet. Git output is streamed and its subprocesses share a 750 ms deadline.
Untracked-file hashing stops at 4,096 files, 1 MiB per file, or 8 MiB total;
incomplete fingerprints remain visibly partial.

Capture uses the existing index lock without waiting behind a rebuild. A busy
or unreadable store causes a best-effort miss. A packet captured before the first
index build does not mark the search index ready. The hook still requests the
usual background index warmup.

## Privacy and supported hosts

The source must declare the exact native session ID and workspace supplied by
the hook. Unknown formats, missing transcript paths, mismatched identities, and
files that change during reading cannot produce a successful capture.

| Host | Capture | Packet handed back |
|---|---|---|
| Claude Code, Codex | `PreCompact` reads the JSONL transcript | next session-start, prompt or tool hook |
| TRAE CLI | `PreCompact` names the rollout; 0.207 writes its turns as `history_mutation` records, so the TRAE reader takes it rather than Codex's | next prompt or tool hook |
| CodeBuddy, WorkBuddy | `PreCompact` reads the transcript | next prompt |
| Muse Code | `PreCompact` names only the session; its log is found by the id | next prompt or edit |
| opencode, Kilo CLI | the plugin's `experimental.session.compacting` names the session; deja reads it from `opencode.db` / `kilo.db`, which keep the compacted turns | next prompt, appended to the user turn. The summary request gets neither recall nor the packet: on 1.x the plugin skips it, and 2.x never runs the context hook for it |
| Qwen Code | `PreCompact` names `chats/<id>.jsonl`, read by the Qwen reader | next prompt or session start |
| Kimi Code | `PreCompact` names only the session; the hook entry adds `--harness kimi` and the session's `wire.jsonl` is found by its id. Kimi waits for the hook before compacting | next prompt |
| Grok Build | `PreCompact` names `updates.jsonl` | next `PreToolUse` (Grok drops what the prompt hook prints) |
| Copilot CLI, VS Code Copilot Chat | `PreCompact`, one entry in `~/.copilot/hooks/deja.json` for both, names `session-state/<id>/events.jsonl` or `GitHub.copilot-chat/transcripts/<id>.jsonl`; the file's layout says which, so a VS Code profile outside the scanned roots still reads | next prompt or tool hook |
| Cursor CLI | `preCompact` names the agent transcript and the workspace roots | next `beforeSubmitPrompt` or `preToolUse` |
| pi, omp, Senpi, gajae-code, prime-agent | the extension's `session_compact` sends the session file; it fires after the compaction entry is appended, and the file keeps the turns before it | next prompt |
| OpenClaw | the plugin's `before_compaction` sends the session file when the run compacts itself, or the session id when the gateway does; both in the agent's workspace | next prompt |
| Cline CLI | the plugin's `onEvent` sees the status notice that opens a compaction and names the session. Cline does not wait for it, but `messages.json` keeps every turn and the summary goes to `<id>.compaction.json` | next request of the run: the prompt builder asks again after a compaction |
| DeepSeek Harness | the plugin flushes the session on `compaction/start` and names it; the session log keeps the turns the summary shadows | next step's request |
| Hermes | the memory provider keeps the turns from `on_pre_compress` and passes them on `on_session_switch` with `reason="compression"`, under the session id the next turn uses | next turn's prefetch |
| Gemini CLI | none before: `PreCompress` fires on every compression attempt, compacting or not. The transcript keeps the old turns and records the rewrite that drops them, so the next `BeforeAgent` finds the compaction there and reads the session as it stood before it | that same `BeforeAgent` |
| Antigravity | none before: there is no compaction event. A compaction writes a `CHECKPOINT` step and keeps the steps before it in `transcript.jsonl`, so the next `PreInvocation` finds it there and reads the conversation as it stood | that same `PreInvocation` |
| Crush | none before: Crush fires only `PreToolUse`. Summarising points `sessions.summary_message_id` at a new message and keeps the turns before it in `crush.db` | the next tool call |
| goose | none before, and goose drops what its hooks print. Compacting keeps the old messages with `agentVisible: false` and adds an agent-only note, "Your context was compacted", so the turn's `Stop` finds it in `sessions.db` | that `Stop`, as its block reason: the one hook answer goose puts in front of the model |

On opencode the packet rides one request, like per-prompt recall there: the
plugin adds it to the copy of the turn opencode sends, not to the stored turn.
A capture read through the registry has no byte offsets, so the
raw-actions-to-first-edit metric below counts only the tail-read hosts.

Hosts with a compaction event that only forget what the session was shown:

| Host | Why no packet |
|---|---|
| TRAE IDE | blocked: the session store `database.db` is SQLCipher-encrypted and its key lives only in the IDE process, so there is nothing deja can read the turns from |
| Hermes without the memory provider | `hermes-auto` does not set `memory.provider=deja-memory`; the provider path above is the one that captures |
| Cline extension (VS Code) | blocked: its file-hook adapter maps TaskStart, UserPromptSubmit, PreToolUse, PostToolUse and TaskComplete and nothing on compaction (extension.js 4.1.23, lines 4365-4367), and its plugin loader fails for want of `jiti` in the VSIX |
| Amp | blocked: the plugin events are `session.start`, `tool.call`, `tool.result`, `agent.start`, `agent.end` and `changes.prompt` (`@ampcode/plugin` index.d.ts:1887-1894); `thread.compact` appears nowhere in the 0.0.1791360091 bundle, and a turn cannot run against a stub because inference goes through Amp's own API |
| Command Code | unverified: its mods get `compaction_start` and `compaction_done` (mod-builder reference api.md:81 in 1.77.0), but no stand has run a mod yet, so it is not wired |

ZCode and CodeWhale have no compaction event deja can hook, and no store deja reads a compaction from.
Roo Code, Continue, Zed and aider have no hooks at all.

Reasonix is handled differently. Its extension receives the turns being folded,
builds the same packet from them in process, and hands it to Reasonix's
summarizer as guidance, so the summary keeps it. Nothing is stored in the
manifest for it and there is no recovery packet on a later hook; the next turn
gets the ordinary digest again.

Stored text uses the existing redaction pass; `DEJA_NO_REDACT=1` retains its
documented opt-out behavior. `DEJA_RECALL=off`, automatic-recall policy, ignored
directories, excluded projects, and forgotten-session tombstones apply to this
path. `deja forget` removes matching packets, including a packet whose session
has not yet been indexed, and prevents it from being captured again. As with the
rest of the index, redaction does not remove all sensitive prose and storage is
not encrypted.

## Measuring recovery

`deja stats` reports locally observed raw tool calls between capture and the
first explicit editing tool, excluding the edit itself. It shows the sample
count, median, and 75th percentile, separately from pending and unmeasured
captures. The metric is reconstructed from the existing bounded usage log;
measurement events contain identifiers and counts, not transcript text.

The count comes from native transcript tool invocations, including tools that
do not run a deja hook. It does not count normalized message records or hook
invocations. A later editing hook can recover the first-edit boundary already
recorded in the transcript. Rewritten logs, unreadable evidence, or an interval
outside the bounded transcript window remain unmeasured. These observations
measure local recovery cost; they do not establish a speedup or compare against
a built-in baseline.
