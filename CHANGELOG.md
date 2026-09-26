# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- The compaction packet carries a short "keep until closed" list: questions waiting on the user, open #N items the agent named, verdicts, and rechecks promised for later. Each line stays across compactions until the transcript closes it (a `gh pr merge N`, "#N merged", a user reply). On 189 real compactions the summaries dropped 35% of open #N items, 71% of verdict lines and 97% of deferred rechecks; the list holds 72% of the dropped #N that were still open, is 274 tokens median, and was right on 34 of 40 hand-checked lines. It takes at most 40% of the packet. Claude Code and Codex only, where the packet is captured.
- A recall page whose answer is a fragment of one long session now says which session to open: "Session <id> matched N times and only three of them fit here — call recall_context with that id to read the rest." A hit quotes at most three of its matches, and the only follow-up the page offered was `offset=` — more sessions, never more of the one already found. Over eighteen questions on a live harness the line cut billed tokens 23% to 51% and gained two correct answers of twelve; it costs 149 bytes and appears only when a served session matched ten times or more.
- `deja bench context` measures the same chains a second time with their history folded into one long session each, and prints it as its own table. Every corpus in the benchmark filed history as many short sessions with one fact each, which is not the shape a real store has: on this machine's own store every recall page's deepest hit matched between 24 and 23,278 times. Folded, the session-start block carries a quarter of a chain's facts instead of half, in 89 tokens instead of 360 — the same content, reached less often because it is filed in one place. The existing rows do not move; the second corpus is built and indexed on its own.

### Fixed
- `deja fix` and the MCP `fix` mode say when what ran after an error was withheld as irreversible. With only a merge, a force push or a deletion on file, the CLI answered that the line matched nothing and offered the same line back as the closest one it held, and MCP said no session ran a command after it. Both now say the remedy exists and is not handed over; MCP still adds what sessions said about the error.
- "X is not on this machine" is no longer said about a program that is on PATH. The sightings stay on file after the program is installed, and the line kept repeating them: on one machine every one of the 56 such lines shown for docker and shellcheck came after the binary was there. The check only silences — a program the hook cannot see may still be missing for the agent's shell, so not finding it changes nothing.
- The warning before a command ("Last time this machine ran X it ended with: …") no longer lays a chain's error at its first program. The error came from whichever part of the line failed, so `gh pr checks` was on file as ending in a failing Go test and `git fetch` as ending in zsh's "== not found". A failure is now recorded against the first program only when everything after it just filters its output, and zsh's "no matches found" only against the part that held the glob. Over 835 warnings shown in real transcripts, 38% described something the command itself does; with the rule 90% do. It drops 486 of the 514 wrong ones and 75 of the 321 right ones, the errors of chains that did fail at their first program.
- A command that cannot be taken back is no longer offered as the fix for an error: merging or deleting through `gh`, deleting or force-pushing a branch, `git reset --hard`, dropping a stash, `git clean`, a recursive `rm`, and changing a cluster or touching a production namespace. The remedy is whatever a session ran next after the same error, and that is often just its next step. Over 396 real hints on one machine, 27 were such commands; 25 had nothing to do with the error they answered. A pair behind one of these still answers.
- The compaction packet is captured for a session that moved between directories. A transcript whose tail named two working directories was refused as a different session, so the long sessions that switch worktrees got no packet: 9 of the 20 largest Claude transcripts on one machine. The session id still has to match; the directory is now the newest one (#4031).
- A session's conclusions no longer include deja's own recall echoed back or a bare "ok". The last message was always a candidate, so a session that answered from memory had "deja-vu recalled: …" served as what it settled, and a session that ended on "done" had that. On a 2,285-session store that was 132 and 104 of 2,804 served lines; now 0 and 3, and the 194 sessions left with nothing had nothing else to give. A reply that opens with the credit line keeps the work after it.
- `recall` given a session id now keeps to a budget. It returned the whole digest before any cap applied, so on a 1,292-session store 7 of the 20 largest sessions came back over the 8192 bytes `recall_context` promises for the same read. It now gets that budget and the same line saying the digest was cut (#4023).
- An answer that had to drop one of the query's words no longer says its sessions hold every word of the query. The word forms line announces the drop — "zstd (ignored: no session matches it with the rest)" — and two lines below it the count, the lead and the per-session marks all claimed the whole question had matched. On this machine's store, eight questions about decisions that were never made came back that way, each with 1 to 5 sessions tagged as holding it; they now get "No session is about this" instead. Answers where every word is present are unchanged, and the three benches do not move. It did not change what a model does with them: over the same eight questions on a live harness, the invented decision was asserted 8 times of 8 with the false claim and 6 of 7 without it.
- On the one-tool MCP shape, `mode: "search"` and `mode: "digest"` were accepted and then answered "query required", and `mode: "recall_context"` — the name every block deja injects tells an agent to call — was rejected outright. The field that carries `q` is now keyed by the call a mode resolves to rather than by the mode, so all ten modes answer. An agent following deja's own instruction was spending a call on an error.

### Changed
- The line a failing command gets no longer offers what another project ran after the same error. The lookup was machine-wide so an environment fact would answer everywhere, but a pair names the project it came from, and what that project did next was almost never about this failure. Over 396 of these lines on one machine, read against the error they answered, the ones from another project addressed it 6 times of 104 and were unrelated 90 times, and 9 of them were commands that could lose work (a stash drop, a force delete, a production namespace). Keeping to the current project raises the share that addresses the error from 29% to 38%. A pair recorded without a project still answers everywhere, and `deja fix` is unchanged.
- Blocks after a session's first no longer carry the "matched on wording, not meaning" caution. It was there so a wording match would not be taken for an answer, and measured against that on a local 9B over 40 cases where the recalled history is about a sibling subject and holds a value nothing else does: quoted unhedged as the answer 12 times of 40 with the line, 10 of 40 without it. Dropping it takes 100 tokens off a session over real prompts, with the payload byte-identical. The untrusted-data frame stays on every block — dropping its sentence doubled how often a directive planted in recalled text was obeyed, 8/48 against 19/48.
- Every surface that tells an agent when to recall — the MCP instructions, the guidance block, both skills and the Hermes provider — now names two triggers that are not questions: the user stating that something of theirs already exists ("I already have X", "we use Y for this"), and the agent being about to say that something on this machine does not exist (#4004, #4005). The trigger lists were all questions, so a statement matched none of them. Cost is 66 tokens in the guidance block and 67 in the MCP instructions; the longer version lives in the skills, which load only when they are used.
- `scripts/denialrate` counts how often an agent denies something the index could have answered, and says the rate is not measurable lexically: over 2817 sessions, 457 denying turns hit 50.6% against a control of 50.8% when the denying sentence has to name what it denies, and the loose rules put the control ahead (#4006).

## [0.21.2] - 2026-09-24

recall stops answering with the session that is asking. An agent's own
transcript is in the index while it is still being written, so a question put
mid-session could come back as its own best match, with the session that held
the answer off the page. `deja secrets --scrub` rewrites the transcripts that
still carry a credential, and `fix` hands over what a session said about an
error when nothing was run after it.

### Added
- `deja secrets --scrub` rewrites the transcripts that still hold a credential: the same `[redacted:kind]` marker the index uses goes where the value was, the original stays at `<file>.deja-backup-<stamp>`, and `deja index` afterwards drops the finding. It replaces only the kinds the report names, refuses a transcript an agent is in or one written in the last two minutes, and prints how many findings it could not reach — a store with no per-session file cannot be rewritten at all, which on one machine was 36 of 88 findings. `--dry-run` says what it would do (#3823).
- `deja forget --list` marks a tombstone whose session is gone everywhere — in neither the index nor any store — as suppressing nothing. A leftover from a session deleted on disk printed exactly like one somebody forgot yesterday. The row still starts with its id and the list is still one line per tombstone; a machine with no leftovers sees no change.
- `deja sources` says when a store holds transcripts the index has never read, in the words `deja doctor` has used since #3747: ``(2 transcripts never read — `deja index`)``. It is the command people run first, and its session count read as "nothing written yet" rather than "files never opened". A store with none says nothing.
- The line before an edit hands over the command those sessions ran, when the transcript saw it pass: `store_test.go has been worked on in 2 sessions — ran here and passed: ` and the command. A file two sessions agree on clears the same bar a decision does.
- opencode gets the point-of-action line at all. Both plugin shapes wired their tool seams to a tool name, so a read produced nothing there; the file action now goes out from `tool.execute.after`, which is the only seam opencode has that reaches the model. Measured on a 325-file fixture: the same task cost 40k tokens against 70k with deja and no line, and 136k with no memory.

### Fixed
- Tab completion offers `deja recap` and `deja tests`, which shipped in 0.21.0 and were in neither the bash nor the fish script. The command list is substituted from one place now, the way the harness and role lists already were — bash and fish each carried it twice and PowerShell three times, and each copy drifted on its own (#3976).
- recall no longer answers with the session that is asking. An agent's transcript is indexed while it is still being written, so the best match for a question asked mid-session could be the question itself, with the session holding the answer pushed off the page (#3945, #3965). Every hook that carries a session id now stamps it beside the index, and the MCP surfaces keep those sessions out of the search for twenty minutes. Nothing changes on the CLI, or on a machine with no hooks wired.
- `fix` answers with what the sessions said when nothing was run. A session that wrote the remedy out in words holds no error/command pair, so the mode reported "no session on this machine ran a command after that error" over an index that held the answer; it now falls back to a recall over the error text and says which of the two answers it is. In a 12-run A/B the one deja run that failed the task failed here.
- `deja install codex` no longer refuses a stock Codex config. Codex writes hook trust tables as `[hooks.state."browser@openai-bundled:plugin.json#hooks[0]:subagent_stop:0:0"]`, and the header check cut the line at that `#` as if it began a comment, so the header read as never closed and nothing was written (#3969). Reported and fixed by @h4ckm1n-dev.
- The `.mcpb` bundle describes all seven modes of the `deja` tool. Its manifest enumerated six and stopped before `orient`, so a Claude Desktop install was never told about the mode that answers what a project's past sessions ran and which files they worked in. `docs/INTEGRATING.md`, which another project reads to wire deja in, had the same six (#3985).
- The getting-started page lists every package under `extensions/`. It said "three more" and named opencode, dsh and Zed while the directory grew to seven, and README.md's table stopped at six; OpenClaw, pi, Grok and Kimi Code are now on both, in the install lines `extensions/README.md` already gives, and neither page counts its lists in prose any more. A test fails the next time a package lands without them.

## [0.21.1] - 2026-09-23

A point release for OpenCode 2.0, reported by its users the day after 0.21.0.
The plugin 0.21.0 wrote does not load on 2.0 at all, and a config that keeps
its servers under `mcp.servers` was rewritten in place rather than left alone.
Codex forks stop losing their turns to the parent thread.

### Fixed
- `deja install opencode-auto` writes the plugin the installed opencode can load. OpenCode 2.0 reads only a module's default export and 1.x refuses any default export without `server()`, so one file cannot serve both: the shape now comes from the version on PATH, then from the store, and `deja doctor` reads the other major's file as stale rather than wired.
- A config deja declines to edit no longer costs the plugin too. An opencode config that keeps its servers under `mcp.servers` is still refused, but the plugin beside it is written, so the machine keeps its auto-recall.
- A forked Codex rollout keeps its own thread. It carries the parent's `session_meta` along with the history it branched from, and the later record used to win, so the child's turns were filed under the parent and `deja show <child>` answered "no session matches".
- A message repeated verbatim in one rollout is indexed once whether the file was appended to or rebuilt. The append path had no dedupe, so the same file held three records incrementally and two after a rebuild.
- The README stops promising that what reaches the model is safe to send. Redaction is pattern matching, the security model says so on its own page, and the hero paragraph said the opposite in English and Japanese.
- The comparison page gets three facts right: claude-mem and agentmemory are Apache-2.0, not MIT; `deja blame` is no longer listed as having no equivalent, because ctx ships one this page already mentions; and retroactive indexing is no longer "nine of the ten cannot do it" — agentmemory imports old Claude Code transcripts now.
- The release publishes the OpenClaw plugin to ClawHub again. The job ran on Node 20 against a CLI that wants 22, and it ended on `Not logged in` — the one message the CLI gives both for a missing OIDC token and for a refused one. It now runs on 22 and says which of the two happened. 0.21.0's plugin was published by hand.

## [0.21.0] - 2026-09-22

`deja secrets` lists the sessions whose transcripts still hold an API key, a
private key or a password inside a URL, by kind and file, and never prints the
value. `deja recap` quotes what the last week settled, `deja tests` reads the
build and test runs already in the transcripts, and `deja stats --year` puts a
year of work in one screen. Line-level blame now also answers for lines a
session wrote, not only the ones it replaced, and the written side of an edit is
read from Zed, Antigravity, Copilot Chat, Copilot CLI, Cursor, Kimi and
opencode's diff store. CodeWhale is the thirty-fourth harness, and the Zed
extension is now `deja-mcp-server`.

### Changed
- The stats card leads with what happened to your code. `spans replaced` takes the middle cell from the message count, and the headline stops saying "1 questions". (#3787)
- The incremental index line is a sentence: `deja: re-reading 306 changed transcripts` instead of `incremental index changed_files=306 removed_files=0 sessions=1789`, with zero counts left out. (#3769)
- The Zed extension is `deja-mcp-server` and carries the context server only. Zed's registry takes an MCP extension under a `*-mcp-server` id and no longer takes slash commands, so the `/deja` slash command is gone; the server key stays `deja-context-server`, so a `deja install zed` entry and the extension still resolve to one server.
- A crowded top of the ranking gets a second pass over the best message. The relevance tier already reranks that way on a large store, where the whole-session total drifts; the gate was store size alone, so on a benchmark haystack or a young store the pass never ran even when the first and second place were a rounding apart. It runs now whenever the gap between them is under 30% of the leader, which is the shape the pass exists for. LongMemEval-S hit@1 85.3% → 86.6% on the 470-question cleaned set, preference questions 36.7% → 43.3%.
- A question that names one day prefers sessions from that day. "What did we decide on Monday", "what broke on the 14th" — the date was a word in the query like any other, so a session that merely said "monday" outranked the one written that Monday. The day is resolved from the question and fused with the word ranking rather than replacing it, and it stands down on three conditions measured into it: a span ("last week") is not a day, a counting question ("how many times since Friday") is not about one day either, and a day outside the candidates' own range is not evidence. Temporal questions 81.1% → 83.5% hit@1, total 86.6% → 87.2%.

### Fixed
- opencode 2.0 stores are read. 2.0 renames `session` to `session_v2` and moves a session's turns out of `message` and `part` into one `session_message` table, so every query deja ran came back `no such table: session`: the harness reported unreadable and none of its sessions reached the index — 154 of them on the store in the report. Both layouts are read now, chosen by where the turns are, along with what moved inside a turn: `bash` is `shell` and `apply_patch` is `patch`, a tool names itself under `$.name`, what it printed is the block list `$.state.content`, and the file it opened is `$.state.input.path`. `edit`, which is 2.0's editing tool, carries the text it replaced and the text it wrote, so `deja restore`, `deja files` and line-level blame answer for an opencode 2.0 session — the 1.x reader only ever saw `apply_patch`. A compaction's summary is indexed under the summary role. Checked against opencode 2.0.12 run in a throwaway home: 4 sessions, 14 records, the shell command with its exit status, the file read, and both sides of the edit. Reported by @yourfriendaaron in #3924. (#3924)
- A resumed or forked conversation no longer counts as a question asked twice. A resume copies each message with its original timestamp, so the same question at the same moment in two sessions is now one asking, in the stats headline, the card and the note after the first build. (#3906)
- A relevance answer says how much actually matched. The label meant "nothing matched" but was also put on strict answers with a thin ranking underneath, which was every relevance answer in a 93-query sweep; a quoted query retried without its quotes gets the same correction. (#3816, #3819)
- `deja doctor` measures freshness from when deja last read the stores. An SSH import rewrote the manifest's build time without opening a local transcript, so a machine syncing on a timer was called up to date over transcripts it had never read. (#3775)
- `deja install --auto` on a machine with no agent says what it looked for and what to run next, instead of `no known agent config directories found`. (#3835)
- `deja install dsh` and `dsh-auto` work. The DeepSeek Harness guide printed those names and the binary only knew `deepseek`; a test now checks every `deja install <target>` the docs print. (#3869)
- `deja secrets` names the transcript file for Cline, Copilot Chat and DeepSeek Harness sessions too, which write one file per session under `.json` or `.zstd` rather than `.jsonl`. (#3874)
- An opencode diff file folds into the session it belongs to instead of reading as a second transcript with the same id. (#3800)
- A store the size of a fresh install gets the relevance tail it was built for. A thin strict answer hangs the relevance ranking underneath it so a question whose wording excluded the answer can still reach it — and that was switched off for any store no bigger than the ranking window, which is every new user and every benchmark haystack. It is bounded there instead of absent: a fifth of the store, never more than ten places, and nothing at all below a dozen sessions. LongMemEval-S hit@1 87.2% → 88.1%, hit@5 95.4% → 97.4%; LoCoMo hit@5 85.7% → 90.9%, R@10 → 95.6%.
- The floor under that tail was one session too high for real conversations. At twenty, two LoCoMo conversations of nineteen sessions each — 302 of its 1,982 questions — were answered with the strict hit alone. At twelve: R@10 94.9% → 95.6%, R@20 97.0% → 97.7%, questions the ranking never reaches at all 60 → 45, hit@1 flat either way. LongMemEval, `deja bench prompt|recall|context` and day0bench are unchanged; giving the strict head an absolute lead on a small store was measured instead and is worse (LongMemEval 87.9%), so the tail keeps its bounded promotion. Zed's registry takes an MCP extension under a `*-mcp-server` id and no longer takes slash commands, so the `/deja` slash command is gone; the server key stays `deja-context-server`, so a `deja install zed` entry and the extension still resolve to one server.
- A manifest rewrite is visible to the process that made it. The read-only manifest cache is keyed on `manifest.gob`'s mtime and size, and its own comment said that is a pair the atomic swap always changes — it is not. A rewrite that keeps the size and lands inside one tick of the filesystem's timestamp resolution leaves both identical, and every surface that reads through the cache — doctor's read state, the session count, friction, the brief — then answers from the manifest before it. It surfaced as a test that failed on CI and passed on every developer machine, which is the honest shape of a clock-resolution bug; the writer now drops the cache entry itself, and the stamp stays as what catches a rewrite by another process. A test reproduces the collision deterministically by putting the second write back on the first one's timestamp, and fails when the invalidation is taken out.

### Added
- `deja secrets` lists the credentials your agent transcripts are carrying: grouped by session, with the kind (AWS key, GitHub token, private key, URL credentials, bearer token and so on), the project, the date and the file to edit or delete. It never prints a value; deja's own index was already redacted, and the report says so. `--json` and `--limit 0` for the full list. (#3822)
- `deja tests` prints the build and test runs already in the transcripts: runs per week split into failed, passed and no verdict, then the tests that failed on more than one day. Nothing new is captured. (#3831)
- Install ends with a question built from your own history and already run, in place of a placeholder search: `what did we decide about <two words from your session titles>?`. (#3814)
- `deja blame <path>:<line>` answers for lines a session wrote, not only lines it replaced. 71% of commits delete nothing, so the replaced-text rule could not reach most added lines; the answer names which rule found it, `replaced:` or `wrote this line:`, and quotes the turn the edit sits in. (#3810, #3817)
- `deja blame <path>:<line> --attribution` prints the line answer alone, as one object with `--json`; `--git-note` records it on the commit under `refs/notes/deja`, opt-in and refused when nothing is attributed. (#3812)
- The written side of an edit is read from Zed and Antigravity, which keep it on the tool result, and from Copilot Chat, Copilot CLI, Cursor and Kimi, which were dropping it; opencode's per-session diff store under `storage/session_diff/` is read too. `deja restore`, `deja files` and blame now have something to answer from there. (#3811, #3818, #3793)
- `deja sync ssh` names each phase before it starts, repeats it every ten seconds while it runs and ends with where the time went, so a large push no longer looks hung. The remote's output arrives line by line. (#3813)
- `deja install gjc-auto` wires auto-recall into gajae-code through the extension directory it loads. (#3859)
- The `/deja` reply in dsh, and a new `/deja` command in OpenClaw, carry deja's note after the first build and its weekly count, which only reached the model there before. `deja hook-context --notes` prints just those notes. (#3895)
- `scripts/longmemeval -score <file>` scores a ranking another system produced, with the same arithmetic as deja's own runs and no deja in the loop. (#3795)
- Roo Code, Kilo Code and the legacy Cline extension record what an edit changed, not only which file it touched. Their editor sends the two sides of a change as a SEARCH/REPLACE block under `diff` rather than as `old_string` and `new_string`, so the three readers kept the path and dropped the change itself: `deja restore` had no span to hand back from those stores and line-level blame could not attribute a line to any session in them. The SEARCH body is the replaced span now and the REPLACE body the hashed written lines, with `search_and_replace`'s two literal sides and the whole-file writes (`write_to_file`, `insert_content`) read the same way. Two things are deliberately not recorded: a `search_and_replace` with `use_regex`, since a pattern is not text the file ever held, and a block whose closing marker never arrives, since where it was meant to end is a guess. Both are pinned by tests that fail when the rule is taken out. The shape is Roo's own diff strategy and the task fixture from #3295; no store on hand carries a Roo edit, so this is verified against the format rather than against an index. What the stand did find is that the records these three readers already wrote name a path relative to the workspace, the way Roo's tools take one — so an edit at the root of a checkout was recorded as `loop.go`, and line-level blame, which matches a record to a file by their last two segments, could never match a one-segment path. The workspace is in each store's own task metadata and the records carry the full path now. In a hermetic stand — one Roo task, its own index directory — `deja restore` reports the span, and blame on the changed line went from "no indexed session wrote this line" to naming the session and printing the line it wrote. (#595)
- `deja stats --year` is the last twelve months of somebody's own work with agents in one screen. Counts describe a store; this describes a year, over every harness on the machine rather than one report per tool: sessions and agents, the busiest project and day, the longest session, the files and distinct commands the agents worked through, the replaced spans `deja restore` still holds, how many questions came round a second time, and the three errors the machine kept hitting. Two rules it keeps. Every number carries the arithmetic that produced it — "28 of 838 questions were asked in more than one session", not "3% redone", and "20,989 files, deduplicated by path, from 48,053 records naming one" rather than a figure whose unit is a guess. And it is written to be shown to other people, so the project names, the session title and the quoted error lines take the outbound redaction pass on the way out and the screen says what that removed, the same rule `deja recap` follows. The window takes no filters: `--harness`, `--project`, `--since` and `--role` are refused rather than narrowing the sessions and leaving the record counts whole, which would print a report that does not add up. `--json` carries the same numbers with `work.records` beside `work.files` so a consumer can see the deduplication for itself. The record counts come from one pass over the store rather than one per kind, which is where the cost of this screen is. Measured on a 2,437-session year: 20,989 files, 42,031 distinct commands, 17,120 replaced spans in 1,899 files. (#578)
- `deja recap` says what the last week settled, quoted from the sessions and grouped by project, with the harness, date and session id behind every line. It writes no prose of its own. The output is meant for a PR description or a standup message, which is the wrong place for an address or a home path, so a recap takes a second redaction pass on top of the index's and says what that removed. `--since`, `--limit`, `--json`. (#544)
- CodeWhale's store, the thirty-fourth harness deja reads. It is the Rust terminal agent that shipped as `deepseek-tui` until v0.8.41, not the DeepSeek Harness already here: one pretty-printed JSON file per session under `~/.codewhale/sessions`, whose content blocks are the Anthropic shape the Cline and Claude readers already take apart — so a call becomes a command or a file record, `tool_result` becomes tool output with the error runs kept, and `thinking` is dropped. The pre-rebrand `~/.deepseek/sessions` is read too while the harness is still migrating it, unless `$CODEWHALE_HOME` says otherwise, which is an isolation boundary in CodeWhale's own resolver. The file carries no per-message timestamps, so the session's own start is the clock at a millisecond a record and two identical turns stay two. `deja resume` hands a session back with `codewhale --resume <id>`, verified against 0.9.13's own flags.
- The privacy page in Chinese, which is the page that decides whether a reader installs anything at all. It carries the whole boundary rather than a summary: what is indexed (the conversation and what the agent did, the compaction packet on two harnesses), that indexing and search have no network path and which three commands are the exceptions, the full redaction list including the prose-password rule from 0.20.2, and every control — exclude a project, exclude a whole store, forget, the redaction report, the injection log, trust scopes. Including the two things a privacy page has to say out loud: pattern matching is not secret detection, and a credential that reached a transcript was already sent to the model provider, so rotate it. The Chinese guide is nine pages now against six this morning.
- A page for deleting Kilo Code task history, which is two stores that know nothing about each other: the VS Code extension writes a directory per task under `kilocode.kilo-code/tasks/<id>/`, and the CLI keeps its own SQLite database at `~/.local/share/kilo/kilo.db`. Clearing one leaves the other, which is how a conversation somebody meant to remove stays on the machine. The page names both, says where `globalStorage` sits on each host — Code, Insiders, VSCodium, Cursor and Windsurf each keep their own, so one extension can have several unrelated task trees — and what a task directory is the only record of. Six problem-shaped pages now, against two this morning.
- A page for deleting opencode session history, which is the store shaped least like the others: one SQLite database holds every session, every message and every part, so the unit of deletion is the whole history rather than a conversation. The page names the three tables and their joins, says to delete from the client because the database is live state it owns, and carries the two things people get wrong — removing rows does not shrink a SQLite file, since the freed pages are reused, and there is no archive behind a removed session. That makes five problem-shaped pages in the guide against two this morning.
- Two more pages for the questions search actually asks, and the Claude Code one in Chinese. `Delete Gemini CLI chat history` answers something no documentation covers: the chats sit under `~/.gemini/tmp/<project-id>/chats/` in a directory named after an id rather than a path, so the page gives both ways to map it back — `~/.gemini/projects.json` and the `.project_root` marker — names the prompt log beside them that deleting the chats does not touch, and warns that the same `tmp` tree holds Antigravity's store on some installs, which is why deja reads the two apart. `Session files on disk`, the page that answers how to delete Claude Code sessions and how large `~/.claude/projects` gets, now exists in Chinese with its numbers intact — 875 files, 1.6 GB, a 304 KB median against one 586 MB outlier, and the 30-day sweep nobody is told about. The guide's problem-shaped pages went from two to four; 22% of the readers who reach this repository open the Chinese README, and the Chinese guide went from six pages to eight.
- A guide page for deleting VS Code Copilot Chat history, which is a question with a real answer and no documentation anywhere. Two pages of that shape existed — "delete Codex sessions", "delete Cursor chat history" — against thirty-three stores. This one carries what the audit for #3637 established: **two stores**, `chatSessions` on older builds and `GitHub.copilot-chat/transcripts` on newer ones, both present on a machine that has been through the upgrade and holding different chats; the `workspace.json` beside them is what turns a hashed directory back into a project name. The numbers are from one real machine — 179 workspace directories, 28 with `chatSessions`, 5 with transcripts, and on VS Code Server 1.137 no `chatSessions` at all with 47 transcripts holding eleven weeks of chats, which is why "my history is gone" after an upgrade is usually "my history moved". What deleting costs is stated too: 11 of those 47 transcripts have no user turn at all, so they are an agent run and the only record that the work happened.
- The page that answers "where does my agent keep its history" exists in Chinese. Six of the guide's fifty-four pages were translated, and the store table was not among them. The table is generated from the English page rather than retyped, so the thirty-three paths cannot drift apart and only the format column is translated; the `hreflang` pair is declared on both sides, since one side alone is discarded and the two pages then compete as duplicates of each other. Both sitemaps carry it — the text one is the second way in, after Search Console read the XML as "could not be processed" twice.
- The page that answers "where does my agent keep its history" lists every store, and the guide stops undercounting. Its table held **25 of the 33** stores: Kilo Code, Kiro, Cherry Studio, Command Code, ZCode, Kimchi Coding, gajae-code and Senpi were missing, so the page whose whole purpose is completeness answered nothing for eight agents. Its lede said "twenty-five agents, one table" — and that sentence is why nothing caught it: the word check pins seven named documents, the digit check reads only numerals, and a count spelled out in prose anywhere else was unchecked. The check now covers every page in the guide, which immediately found twelve more: "twenty-five harnesses" on the find-a-session, lost-context and resume-a-session pages, and "thirty agents" on nine per-agent pages. A relative count is left alone, because two shapes are honest — "and twenty-seven more agents" counts from the ones already named, and Hermes's "the other thirty-two" counts every store but its own — and both move with the total without equalling it. (#3750)
- `deja blame` says when the line you typed is not a line. `a.txt:99` has said `the file has 3 lines` since #3726; six other specs said nothing at all and answered about the file as though no line had been asked for. Two shapes, both silent: digits that name no line — `a.txt:0`, and a number too large for `strconv.Atoi`, which fell through to line 0 — and a suffix that is not digits at all, where `a.txt:abc`, `a.txt:-1`, `a.txt:+3` and `a.txt:2.5` were taken as the whole filename and answered `no sessions mention a.txt:abc`. Each now prints one line before the answer — `deja: "abc" is not a line number — answering for the whole file` — and the file-level answer follows as before. What gets searched is unchanged, because a colon is legal in a filename: the note fires only when the part before it looks like a filename and the part after holds no separator, so `notes:draft` and `C:\work\pool.go` stay silent. (#3738)
- `deja doctor` says when a store holds a transcript the index has never read. The audit that found this had to be written by hand: the live index on a real machine tracked 1,533 files and had **never tracked ten** — five senpi sessions written eight weeks earlier and five Copilot Chat transcripts from its second store — with every surface silent about it, because a store's session count of zero reads as "nothing written yet" rather than "five files never opened". None of the ten was unreadable, ignored or forgotten: each parses into a session, and one ordinary pass over a copy of that index adopted all ten in 35 seconds. What kept them out is the shape of the batch — a Copilot Chat transcript is not an appendable kind, and one new file of a kind with no offset parser makes the whole pass replacement-grade, which only a search, a recall or `deja index` performs. The hooks never index, and that machine is hooks: 101 usage events one day and 50 the next, every one of them a hook, a tool line or a déjà vu moment. The row now reads `(2 files, 1 indexed session, 1 transcript never read — `deja index`)` and goes quiet after a pass, so it is a prompt rather than a complaint; `--json` carries `never_read` for the same reason it carries the files-against-sessions pair. Why a store can sit unread for weeks is #3747, still open — this is the surface that makes it visible rather than the fix. (#3747)
- The one rule deja applies unasked — keep a background agent's own job tree out of recall — reaches a working directory a harness folded into one directory name. omp records a session under `-.claude-jobs-<id>-tmp-dsh-work` and calls the project `dsh/work`, so neither spelling contains what `*/.claude/jobs/*` matches: on a real store of 2,721 rows, 317 name a job tree and **12 were served**, every one from that shape. What they are is the point — transcripts from a stand a background agent ran inside `.claude/jobs`, the class #2050 measured at 207 of the 300 most recent sessions, one of which outranked the session that had settled the same question. A directory segment beginning with a dash is now decoded before the literal match, which is the shape every one of these encoders produces because the path it encodes starts with a separator; a name that merely carries dashes (`my-jobs-list.jsonl`) does not begin with one and cannot be decoded into a match. Measured after the change on the same store: escapes 12 to 0, rows hidden 305 to 317, and nothing hidden whose path and project never say jobs. A rule someone writes themselves reaches the encoded form too, since that is where their work is as well. (#3746)
- The index holds everything the stores hold, and that is now a measured claim rather than a slogan. Audited read-only against this machine's own stores: **3,027 sessions on disk across 21 of them, 2,726 in the index, and all 301 of the difference accounted for** — 290 forgotten through `deja forget`, 11 carrying two messages or fewer, none unexplained. The other direction is clean as well: 0 rows whose session no longer exists on disk, so nothing answers from a transcript that is gone. The audit is what turned up the ignore-rule gap above.
- The rule the cross-agent claim rests on has a test naming it. Two agents working in one checkout do not agree on what the project is called: measured over a real store of 2,331 sessions, of 170 pairs of sessions from different harnesses that edited the same absolute file, **26** agreed on the project string and **144** did not — one name contained the other in 84 cases (`src/pool` against `pool`), one was a path where the other was a bare name in 52. What carries the claim anyway is the rule that admits a session by the files it touched under this checkout, whatever it was recorded under, and over 515 handovers in that store the session before this one was never missing from the candidate pool: the three the block shows carried it in 496, and 13 of the 19 misses are subagent runs the digest drops on purpose. Nothing covered two harnesses disagreeing, which is 85% of the real pairs, so deleting the rule left the suite green; the test builds that disagreement from both stores and checks its own premise, so it cannot pass for the wrong reason if the harnesses ever agree. No behaviour change — the measurement found nothing to fix, which is the other half of the result. (#3744)
- A publish on Windows stops losing to a reader that polls. `internal/atomicfile` renames a temp file into place and retried a flat 20 times, 5 ms apart, which is 100 ms — and a reader calling `os.ReadFile` in a loop is exactly what these files have: Go's own `os.Open` asks for `FILE_SHARE_READ|FILE_SHARE_WRITE` and not `FILE_SHARE_DELETE`, so any ordinary Go reader refuses the rename for as long as its handle is open. A constant wait keeps step with a loop, so the writer woke inside the reader's next open on every attempt and spent the whole budget without landing: three of four concurrent writers were denied on the windows leg, which is where `main` went red. The wait doubles from 2 ms to 40 ms over 30 attempts, about 1.3 seconds, and carries up to half of itself again at random — the jitter is the part that breaks the phase lock, and it is pinned by a test that fails when the wait becomes a constant again. Unix still lands on the first attempt and pays nothing. The file this protects is the warmup status: a failed publish there leaves the previous build's contents in place and a surface reads that no build is running while one is. (#3743)
- The harness count is the registry's in every document that spells it, and two of the three ways it can go stale now fail a test. #3649 bumped the word by editing its first half and left the tail behind — "the thirty-three-six coding agents" shipped for three releases in the harnesses page, its three meta tags, `llms-install.md` and this repository's architecture document, and the test that reads those files could not see it, because it removes the wanted word before looking for stale ones and `thirty-three` out of `thirty-three-six` leaves `-six`. `docs/llms.txt` was wrong on its own terms and checked by nothing: the word test cannot take that file, since it legitimately says "and twenty-seven more" beside six named agents, and the digit test only reads `.html` and `.md` — so it carried "each of the thirty-two agents" and "each of the thirty-one agents", two counts apart, in one file. The tail and that phrase are both pinned now, each verified by putting the old text back and watching the test fail. The GitHub description said "and 20 more coding agents", which is what search results showed before anyone reached a document at all. (#3741)
- The documents that describe the MCP server describe the one it is. Six tools became one `deja` tool with a `mode` in #1298, and eight documents went on listing six: the architecture page put them under a heading that says `tools/list`, the Zed extension promised a table of six entries in the agent panel where a reader sees one, and the guidance deja writes into every harness — the skill file and the session-start block both — named `recall_context` as a thing to call. The old names still answer a client that has them wired, which is why nothing broke and nothing complained. The modes in the check come from the tool's own schema rather than a second list, so a mode gained or lost reaches the pages: putting six tools back, and adding a seventh mode, each fail it. Two prose claims went with it — the footer on 103 pages said deja was "fully local" where four network paths exist, and the share card quoted a latency of ~1.5 ms that no run produced (the measured pair is 0.7–0.8 ms in process and 0.25 s end to end over 5.7 GB).
- The Chinese pages carry the scoped claim too. Their footer said 全部在本地运行 — "runs entirely locally" — on all eleven of them, the same absolute the English footers dropped, and the getting-started page repeated it in its meta description, its og and twitter copies and its structured data. What pinned it was nothing: the privacy rule matched "nothing leaves / is sent / is uploaded" and never the shortest form of the claim. It now reads `fully local`, `entirely local`, `completely local`, `100% local`, 全部在本地 and 完全本地 as the same promise, in the claim's own sentence, and putting any of those back into a footer fails it.
- Home directories in test data belong to nobody. Fifteen paths carried the account of the machine they were written on, spelled `-Users-<account>-deja-vu` — the form a harness makes of a project path — which is invisible to a search for `/Users/<account>`. The rule that keeps them out asks the passwd entry for the account it is running as, so no real name is written down in order to check for one, and a build account is skipped because `/home/runner/work/...` stands in an ignore-rule fixture on purpose.
- The documentation says what 0.20.2 shipped. The prose-password rule is in the security model, the privacy page and both READMEs, with the gate that keeps it off ordinary sentences; line-level blame has a section on the auditing page with what it answers for and what it deliberately does not print; `deja how` and `deja files` say that they answer from the project of the working directory, with `--all-projects` for the machine; `deja index --quiet` is where someone keeping the index warm from a shell profile will read it. The architecture document's source table listed 24 of the 33 stores and its redaction paragraph was four rules behind. The contributing guide now states when the Windows leg runs and why a directory mode bit is not a permission there, which is the trap two pull requests hit this week.

## [0.20.2] - 2026-09-18

### Added
- The brief reads the usage log once instead of four times. It printed four figures — today's recalls and bytes, the week's recalls, the week's déjà vu moments, and today's source volume — and called a reader per figure, each of which opened the file, scanned it line by line and JSON-decoded every line including the ones outside its own window. Over a log the size the issue measured on a real machine (3,200 lines, a busy fortnight), the four readers cost **12.2 ms and 76,880 allocations** against **3.1 ms and 19,220** for one pass: a benchmark of both shapes ships beside the change. The week note, which a session-start hook writes, went from two passes to one on the same reader. The statusline and the session-start hook had already been folded into it (#2224); this is the rest. Speed is the smaller half: separate passes are separate snapshots, so an event recorded between two of them landed in the second and not the first, and the brief could print a "today" figure and a "this week" figure that were never true at the same moment. `TodayDemand`, `Week`, `DejaVuWeek` and `TodayRaw` are wrappers around the single pass now, so every other caller keeps its reader and gets the same numbers. (#1576)
- `deja doctor` says when the Grok Build plugin is behind. Kimi users have been told since #1721; Grok's plugin is installed from a marketplace entry that pins a commit, so an installed copy stays where the pin left it — the bundle went to 0.2.0 while every installed copy was still 0.1.0, and nothing on the machine said so. The row now reads `plugin` with "v0.1.0 installed, v0.2.0 ships with this deja — `grok plugin update deja`", on both the auto-recall and the MCP lines, since the plugin carries both and stands down where the installer already wrote them. Behind, never merely different: `grok plugin install ./…` installs a working copy that may legitimately be ahead of the bundle, and deja cannot tell one from the other by the files it can see, so a copy at or above this version just reports its number. The installed directory name is generated, so the copy is found by the name in its own manifest rather than by a path; a directory holding another plugin, an unreadable manifest, and a machine with no Grok at all are all silent rather than errors. A test holds the constant and the shipped manifest together, which is the reason `TestKimiManifestsAgree` exists. (#1828)
- A password stated in prose is masked. Every rule in the redactor wants a delimiter — a colon, an equals sign, a flag — and a person telling an agent a password writes none, so "also the admin password is hunter2-2026, do not put it in code" went into the index verbatim and `deja show` and `deja ctx` read it back in the clear. Found by seeding four obviously fake credentials into a transcript and asking every surface for them: three were masked on the way in — the connection url, the AWS key, the `.env` assignment and the key named mid-sentence — and the fourth was this shape. What keeps the rule away from prose is the one the international pass already rests on: the value has to carry a digit or a symbol and it stops at the first space, so "the password is wrong" and "the password is the same as staging" are untouched. Index format 49, since redaction runs at ingest and a store built before this keeps the text until it re-reads its sources. (#3729)
- A mistyped flag gets the same help from eleven more commands. `deja search --limt x` has named the flag you probably meant since #755 — folding a near miss into the query turned a working search into "you have no such memory" — and the other 22 refusal sites just said no, so someone who typed `--limt` at `deja show` had made the same mistake and got less help. `index`, `show`, `forget`, `stats`, `doctor`, `view`, `log`, `remember`, `restore`, `promote` and `handoff` now answer through one helper: `<command>: unknown flag "--limt" — did you mean --limit?`, with the plain refusal when nothing is within a prefix or two edits. Search gains the command name it was the only one to leave out, so two refusals side by side say which parser spoke. The matching rule is unchanged and still deliberately narrow — a bare word is not a flag and a single dash may be a value — so a dashed query term still searches. The suggestion is only as good as the list it is drawn from, and the list sits away from the parser that reads the flags, so a test holds the two together: whatever flag tokens a converted parser mentions, its list has to name. The remaining 11 sites carry their own guidance (`sync export` names the three flags it takes, `blame` explains `--`) and are left alone. (#1829)
- `deja index --quiet` for the runs nobody is watching. The command always printed a line and there was no way to ask it not to, so a shell profile or a session hook that keeps the index warm printed `deja: index is up to date (…)` over the prompt on every new shell — the kind of thing that gets a tool uninstalled rather than reported — and the workaround, `deja index >/dev/null`, also swallows the errors you would want to see. The flag covers both the incremental run and `--rebuild`, including the live progress display, since they print through the same sink and a reader would not expect one to stay noisy. Quiet is the success reporting and nothing else: a store that could not be read, an exclude list that is not applied, an index that came out empty for a reason, and any failure to build at all still go to stderr, because a scheduled run that goes quiet about failure is worse than one that prints a line. (#1827)
- The line-level blame answer says which silence it is. `deja blame <path>:<line>` printed nothing about the line when it could not answer — a line past the end of the file, a directory, a path that is not there, a file outside a repository, one the version control system does not track, an uncommitted line — so the reader could not tell "this line has no history" from "your `:120` was ignored". Each of those now says so in one sentence, with the line count where that is the reason. Found by running the edges against the feature the same day it landed, and it is the rule every other empty answer here already follows. (#3726)
- A full uninstall takes back deja's own launcher directory. `~/.config/deja/bin` stood empty afterwards — the one directory left on a stand where every target had been installed and then removed. #3698's prune walks what the wiring record names, and this directory was never recorded: the launcher writer called `MkdirAll` straight and the remover took the file without walking up. Recorded before it is made and pruned after the file goes, so the config root comes back holding the record and the lock and nothing else. (#3725)
- The first-screen suggestion is picked from what people typed as the task. It was a pair of adjacent words from anywhere in any message, and what that finds is a fragment of whatever was being said: on a 2,000-session store the pick was an idiom nobody would search for. Four sources were scored by what a reader running the suggestion gets back and by whether the phrase is one somebody wrote as a task — a bigram from any message, 18 hits and 0 of the top five carrying it in their own title; the subject of the friction line, 66 and 0; the newest session's title, 50 and 1; a phrase recurring across session titles, **50 and 2**. Titles win for a structural reason: a title is what somebody typed when they asked for the work. The friction subject scores well and is deliberately not used, because the brief prints that wall one line above the suggestion already. Titles the ingest borrowed from plumbing are skipped — a `<teammate-message>` envelope becomes `harness output: …` and no longer looks like an envelope, and on an agent-heavy store that family is most of the titles. The message scan stays as the fallback, so a store whose sessions carry no title of their own still gets a suggestion. (#3714)
- `deja blame <path>:<line>` says which session wrote the line. The version control system says who changed it and when; the session that made the change is what says why, and until now the line number was parsed and thrown away. The rule is the one the measurement picked: the session has to have replaced the same text the commit shows as deleted — an edit record holds what an edit replaced, so a match means that session performed this very change. Over 494 commits and 4 dependency bumps from a month of this repository — time-and-file overlap named one session for 378 commits but attributed **2 of the 4 bumps** to a session that had nothing to do with them; naming the number the commit closes reached 355 and attributed 2 of 4 as well, because the maintainer's own session names the pull request while merging it; the span rule answered for **116** commits and attributed **none** of the bumps. Ambiguity is repository-shaped rather than absent: on another repository in the same store 3 of 17 attributed commits had two candidate sessions, and the last edit before the commit wins. So it answers for about a quarter of lines and says which silence the other three quarters are. No "why" line is printed, and that is measured too: over 81 attributed lines, the session's own conclusion overlapped the change 0 times and its first conclusion after the edit 2 times, so any single line lifted out of a session reads as the reason for a change it has nothing to do with — the session, the text it replaced and `deja ctx <id>` are what deja can stand behind. (#1181)
- `deja blame` stops quoting a tool's write confirmation as the reason a file looks the way it does. Measured over 1,439 paths that sessions on a real store actually touched: of the 7,417 snippets it quoted, **1,567** were a tool's own sentence — "The file … has been updated successfully", a patch receipt — and 3,027 of the 4,835 rows carried at least one. The filter already existed and recall has used it since #2068: `digest.IsAgentArtifact` recognises 1,565 of the 1,567. Tool output only, because a command record starts with `$ ` and is an artifact by that predicate, while what a session ran after touching a file is evidence rather than echo. 540 rows lose their quotes entirely and keep the session, the date and the title. (#3721)
- `deja fix` stops answering an error with the command that produced it. A session did re-run what had just failed and the error went away for a reason of its own, so the pair is honest about what happened and useless as a remedy: `ran next: X` above `after this failed: X`, on the surface the post-error hook speaks from. The two strings are never equal — a stored command carries its exit status — which is why nothing caught it; compared on the command alone, 1 of the 7 pairs served on a real store that carry a failing command is this shape. Dropped in `FixesFor`, where all seven callers come through. (#3720)
- The suggestion on the first screen has to be a phrase that recurs. It was scored on the rarity of two adjacent words and nothing about the pair, so on a store with prose in it the pick was a fragment lifted out of one sentence — a verb and its object, the subject of neither session that held it. Measured on a 2,421-session store: requiring the pair itself in three sessions rather than two moved what a reader running the suggestion gets back from 10 sessions to 18, and 102,558 of the candidate bigrams there appear in a single session against 10,301 in two or more. Two sessions on a store under a hundred, where three of anything is most of the history and the line would go missing instead. What the phrase should be picked from at all is a different question (#3714).
- The handover names the work rather than the scratch. `deja wip`'s five file slots went to whatever was touched last, and on a real store that was throwaway paths — a probe script under a temporary directory, the harness's own scratchpad, a log — in **238** of the 400 most recent sessions with file records, with a repository file that had lost its slot to one in 29 of them. The same paths `deja files` already refuses are refused here, by shape rather than by asking the disk, since this is a leaf package: 189 of the 238 clear it, 180 handovers stop naming files at all because they touched nothing else, and the line names a repository file in 171 sessions against 162 before. What the compaction handover names is what the next turn opens. (#3716)
- `deja wip` names files rather than words. Every whitespace-separated token of a files or edit record counted as a path, so a record carrying a sentence contributed one "file" per word — `files in flight: …/scope_audit.sh, was., it, as, exactly` — and the words crowded the real paths out of the handful that get named. This is the line an agent reads to pick its own work back up after a compaction. A token has to look like a path now: a separator, an extension, or a leading `/`, `~` or `.`. (#3706)
- `deja files` answers from the project it was asked in too. It read the machine and ranked by how much a file was touched, so a generic topic answered with the busiest project's tree: asked inside this repository, `deja files "timeout"` named eight paths — SQL migrations and a Python service from two unrelated projects — and not one of them was here, while the same question scoped to this repository named its four. An agent cannot tell the difference by reading them, which is what makes it worse than no answer. Same default, flags and widening as `deja how`, the scope named in the text and carried as `project` in `--json`, and the project filter is a set now, so a worktree that records its own name is still the project it belongs to. (#3713)
- `deja how` answers from the project it was asked in. It read the whole machine and ranked by how often a command had run anywhere, so the busiest repository answered every question asked in any other: inside this one, `deja how "test"` returned `rtk go test ./...` — another project's wrapper, 40 runs — three times before this repository's own command, which was not in the eight at all. The scope is now the project of the working directory, derived the way `deja wip` and the hooks derive it (worktrees included); `--project` still asks about another one, `--all-projects` asks the machine, the text says which of the two answered, and `--json` carries it as `project`. A project with nothing to say still gets the machine's answer rather than "no command on this machine mentions it" — an agent told nothing exists invents one. The MCP `how` tool takes the same default. (#3705)
- The asked-twice line stops telling a reader their own resumed conversation back to them. A resume, a fork or a share writes the prior turns into a second session file, so every question in it repeats verbatim under a second key — and on a 2,300-session store with a year of history, **all nine** repeats at least 48 hours apart were that: the winner was one opencode conversation under four ids, the same eight asked hashes in each, "asked in four sessions" over four weeks. The signal was already in the manifest, so the test costs no record read: the two sessions' asked hashes minus the one they matched on, a third of the rest in common. Two sessions that share only the question are the case the line exists for and still fire, which is why the matched hash is excluded rather than counted. That store's line now says nothing, which is the honest answer for it. (#3711)
- `recall_context` resolves an id as an id. Every session-start block prints a session id and tells the agent to follow that up with this tool — and an id is also a searchable string, so any transcript that mentions one matched it lexically and the search answered first. Measured on a real store by asking for the 120 most recent sessions by their own id prefix: **24** brought the right session back and the other 96 answered with a different session, stated with the same confidence. Now 120 of 120. `recall` takes the same rule, since it is the tool an agent reaches for first and it was worse: asked for those same ids it answered about *other* sessions 77 times, named the right session 37 and said nothing 6. Id-shaped rather than merely wordless — a digit, a dash or an underscore, which every store's id has and a word does not — so "timeout" and "wiring" stay searches. The block's instruction names the id too, since the other half of the same measurement is that a single distinctive word from the quoted line brings its session back at rank 1 in 3% of cases and three of them in 39%. (#3717)
- Copilot Chat's second store is read, and on a newer extension it is the only one with the history in it: `workspaceStorage/<hash>/GitHub.copilot-chat/transcripts/<id>.jsonl`. On VS Code Server 1.137 there is no `chatSessions` directory at all — the reporter's machine had 47 transcripts holding eleven weeks of chats and deja found zero files — and on desktop 1.136.1 both layouts sit side by side, 28 `chatSessions` directories and five transcripts. The records are a `type`-discriminated event log rather than the `kind` 0/1/2 delta log beside it, so it is its own reader: user turns from `user.message`, replies from `assistant.message`, the files and commands a tool call names, and the project from the `workspace.json` two levels up or from the paths the session touched. A transcript with no user turn at all is an agent run and is indexed — 11 of those 47 and all five here have none, and what the agent said is the only record of that work. Measured on this machine: copilot-chat 17 sessions → 22, and the second probe per workspace directory costs about 20 ms of discovery across 179 of them. Reported with the format, the histograms and both stores' contents by yizhixiaokong. (#3637)
- An install and an uninstall give a reader's `AGENTS.md` back exactly as it was. Grok Build's guidance is a marked block inside a file its owner writes, and the round trip left one blank line behind: install trims the trailing newlines and appends `\n\n` before its block, and the uninstall cut the block alone. 56 bytes in, 57 out — a one-line diff in somebody's dotfiles, from the same pair #2606 fixed on the goose side from the other direction. The trailing run of blank lines still collapses to one at install time, which is the file's state while deja is wired and is now stated in a test rather than left to be found. (#3703)
- The manual kilocode, gjc and Command Code get declares the name of the directory it sits in. All three had it under `skills/deja-search/` — the CLI skill's name — while its frontmatter said `name: deja-history`, and a skill whose name and directory disagree is one a loader may drop or rename: Claude Code requires the match, and Gemini renaming the loser of a collision is how #3665 was found. It goes under `skills/deja-history/` now, the copy deja wrote under the old name comes out on install rather than outliving the fix, and the CLI skill that legitimately lives in a `deja-search` directory is left alone. Found by reading every file deja writes the way a harness reads it, which is now a test: every `SKILL.md` must declare the name of its own directory. (#3700)
- `deja uninstall` takes back the directories it made. On a bare home a full install and a full uninstall left **33** of them standing empty, from the harness roots down to a plugin directory four levels deep — `~/.pi/agent/extensions`, `~/.openclaw/hooks`, `~/Library/Application Support/Code/User/prompts`, `~/.agents/skills`. Two halves: the record kept only the file's own parent, so a writer that creates a tree left everything above the last level unvouched for and the prune from #3239 stopped at the first directory it could not speak for; and fourteen writers removed their own directory and never looked up. Every level a write creates is recorded now, the prune walks upward while each is empty and deja's, and OpenClaw's pair stops reporting `removed` for a directory that was not there. Thirty-three became zero, held by the same walk the files have: after a full round, no directory the record names may still be there. (#3698)
- A config with a UTF-8 byte order mark installs. PowerShell 5.1 writes one by default — `Set-Content`, `Out-File`, a `>` redirect — and editors on Windows may too, and every JSON target refused such a file with `invalid character '<mark>' looking for beginning of value`: a remedy naming a character nobody can see, in a file its owner did not knowingly change, and one a harness may well read, since VS Code's own readers strip a mark. It belongs with the line endings and the indent — read past it, write it back — so the file keeps the shape its owner's tools give it, and a file that had none does not get one. (#3696)
- `deja doctor` stops naming a doubled hook that an install has since collapsed. The repeated-injection row reads what actually happened rather than what a config says, which is what made it the check that found #3421 — and it read the whole usage log, so one bad morning was reported for as long as the log was kept, with a remedy the reader had already applied. Two bounds now, whichever is later: the last time deja wrote its wiring record, since an install is what collapses the entries, and a week, so a doubling somebody fixed by hand stops being named once no new event follows it. (#3697)
- ZCode's writer reads its own hook entries whatever the build is called. #3681 gave six writers that predicate and this one kept the narrow name test, so on Windows — where the test binary is `deja.test.exe` — its uninstall read deja's own entries as a stranger's and left the whole block behind. One invariant now asks every auto target the same question, in the shape the machine in #3681 was in: every entry naming a build that is not the one installing.
- The wiring record holds only targets deja knows. It is written from the arguments, before any of them has been through the installer, so `deja install nosuchtarget` — a typo, or a script listing the names — was kept beside the targets that worked, and every repair after an upgrade retried it, failed, and counted it in the "could not rewire" line at session start. A name already in a record written by an older build drops out on the next install. (#3695)
- Hook lines survive a home directory with a space in its name. The launcher lives under the home directory and a hook entry is a command string the harness hands to a shell, so on such a machine every one of them was unrunnable — 34 lines on one stand, and `sh` says it plainly: `sh: /tmp/deja: No such file or directory`. Auto-recall was dead across twenty harnesses with nothing reported, because doctor skips any path with a space on purpose — the right call for reading somebody else's file and the wrong one for a file deja wrote. Windows is where an ordinary machine meets this: `C:\Users\Name Surname`, `C:\Program Files`. deja quotes its own hook lines where a shell needs it, in one helper rather than at twenty-five call sites, with cmd.exe's quoting on Windows and the shell's elsewhere; an ordinary path is left unquoted. An entry already written in the broken form is recognised as deja's own and rewritten rather than read as somebody's wrapper, so an install repairs a machine that already has it, and doctor can now read a quoted path and report a binary that is gone. The invariant it was found with ships too: install every auto target into a home with a space in its name, and every command line must name a file that is there. (#3692)
- One install at a time. Every writer reads a config, edits it and writes it back, and nothing stopped a second deja landing in the middle: `deja install claude-auto` and `deja install statusline` started together lost one of the two wirings in eight runs out of twelve, both of them editing `~/.claude/settings.json`. Nothing is corrupt — the write renames into place — the later process simply built its content on the file as it was before the earlier one. The pairing is what a machine does on its own: the repair after an upgrade runs an install from the hook path, so a session starting while someone types `deja install` is this race. An advisory lock beside the wiring record now serialises the two verbs, and the repair stands down rather than waiting, since whoever holds the lock is writing the same wiring. A machine that will not give deja a lock carries on without one. (#3691)
- ZCode's uninstall takes back the containers it added. Its two writers created `mcp.servers` and `hooks` without recording them, so a full uninstall left `{"hooks":{"enabled":true},"mcp":{"servers":{}}}` in a file deja had created itself. A container the reader already had is still theirs, switch included — that is gemini's rule and the reason for it holds wherever the block is not deja's. (#3690)
- `deja install --all` keeps the auto layer it finds instead of turning it off. DeepSeek Harness keeps the server and the plugins in one patch list, so its plain target drops the auto row and deletes the plugin — that is how someone steps back from `--auto`, and it is what `--all` did to them unasked, reporting `deepseek: updated`. `--all` now refreshes the `-auto` sibling of any target the record says is wired, after the plain one rather than instead of it. Found by installing every target twice and asking which files changed on the second run. (#3687)
- One text in the shared skill file. `~/.agents/skills/deja-history/SKILL.md` is read by sixteen harnesses, and pi carried a body of its own from before the current one existed — two tools where the shared text describes six, nothing about reading a result marker, nothing about the bounded result window. pi joined that directory in #3657, and from then on `deja install pi` replaced what codex, omp, Senpi, Kimchi, gjc and Zed are told, while the next install of any of those put it back: on a machine with both, ordering decided what every agent read. (#3688)
- A repeat `deja install goose` changes nothing and says so. Goose keeps the slash command and the MCP extension in one `config.yaml`; each writer removes its own block and adds it back, and removing the only entry takes the key with it, so each re-appended at the bottom under the other's — the two keys swapped places on every install and the report said "updated" twice with nothing to point at. When the entry deja wants is already in the file, exactly, the file is left where it stands. The repair after an upgrade runs that install, so a `config.yaml` kept in a dotfiles repository showed a diff every time deja moved. (#3689)
- The status bar is part of deja's own bookkeeping, which fixes five things at once. `recordWiring` skipped the `statusline` target, and with no other target the record was never written at all — so nothing knew deja had wired it. The status bar is also the one wiring that names the binary directly rather than the launcher, so a build that moves leaves it running a file that is not there, on a one-second timer: the repair never reached it, `deja install statusline` called deja's own stale entry the reader's and offered to combine the missing binary with the present one, `deja uninstall statusline` would not remove it either, `uninstall --all` walked past it because a status bar is not a harness, and the uninstall left behind both the settings file it had created and a `.bak` of deja's own wiring it reported as "configs you already had". The entry is now recognised by the pair every hook is recognised by — a deja-named binary running one of deja's subcommands — which is also what `mentionsDeja` was missing. A statusline the reader combined with their own stays theirs, untouched by either verb. (#3684)
- `uninstall` takes back the commands directory it created, and puts back a `/deja` command it replaced. Claude Code's slash command has a writer of its own and it kept neither rule the other command files have had since #2581 and #2600: it made `~/.claude/commands` without recording it, so an empty directory outlived every uninstall, and it removed its file without restoring the one install had replaced — a reader with their own `/deja` got it back as a `.bak` nobody restores. Found by walking every file in a home after install and after uninstall rather than by checking the paths a test names. (#3685)
- `deja install` takes several targets, because `deja doctor` asks for exactly that. The stale-wiring row — the one a person reads when the binary has moved and the repair on the hook path cannot run — prints `deja install ` followed by every target in the record, and on any machine with two of them that line answered "install needs a target". `--all` and `--auto` still stand alone. (#3686)
- Cursor's `/deja` carries its description again. Cursor reads a command's description from the first line of the file and does not parse markdown frontmatter, so deja's entry showed `---` in the one list where a person picks which command to run — while the two skills beside it described themselves. Measured with two probe commands in one palette, one of each shape, at both user and project scope. The harnesses whose own docs specify frontmatter keep it. (#3666)
- Gemini gets no `/deja` command file, because its skills are commands. The rename in #3655 moved the collision rather than ending it, and Gemini said so on its own start screen: "Skill command '/deja-search' was renamed to '/deja-search1'". Everything deja installs lands in one flat namespace there — the MCP server's prompt is `/deja`, the CLI skill is `/deja-search`, the manual is `/deja-history` — so a file beside them is a third entry doing what the other two already do, whatever it is called. Gemini joins the eight harnesses where the skill is the command, and both names the file used to have are taken out on install so the clash does not outlive the fix. A command of the reader's own under either name is left alone. (#3665)
- The Commands section says what it means where deja writes no command file. Ten harnesses make a skill invocable by name, so the skill deja installs *is* the command there and a file beside it would only add a second entry — Gemini proved that by renaming one of the two. Those rows were simply absent, and an absent row reads as "deja has no command here" when the truth is that it is installed under another name. They are rows with the state `skill` now, naming the skill file, and a line under the section says each harness spells the invocation its own way (codex `/skills`, Kimi `/skill:<name>`, Copilot `/<skill-name>`) rather than promising one. The list of those harnesses moved into the product, beside the report that prints it, so the capability test and the report read the same one. (#3667)
- `deja doctor` stops counting qwen's and OpenClaw's own state as transcripts it failed to read. On the machine this was found the rows said `11 not recognised here` for qwen and `1` for OpenClaw while every session in both stores was indexed — and eleven is a number a reader takes as a parser that cannot cope with their history. What it counted was qwen's `<id>.runtime.json`, `meta.json` and `extract-cursor.json`, and OpenClaw's `agent/models.json`: bookkeeping, not conversations. Same call as Continue's `sessions.json` and Kimi's `state.json`. That machine now reports zero unread files across all 33 harnesses, down from twelve. (#3676)
- Hermes's published store paths name the single-file layout as well. `~/.hermes/state.db` is what a machine with one profile has — the reader has taken it since 0.17 and the registry page says so — but the machine-readable `store_paths` listed only the per-profile form, so a reader checking the published list against their own disk found nothing. Caught by walking every documented path on a real machine: of the 33, this was the one that matched no file it should have. (#3680)
- The documented path for Kimchi's store was one no machine has. Its own binary builds `<agent>/sessions/--<encoded cwd>--/<id>.jsonl` in `getDefaultSessionDirPath` — a directory per project, the way pi and Senpi do it — while the registry said the root was flat and the reader's comment said so too. Measured rather than read: a session placed flat is rejected by the client (`No session found matching …`, because its own lookup enumerates the per-project directories), and the same file under the encoded directory opens. deja's walk is recursive so nothing was lost, but the published path, the fixture and the comment were all describing the wrong layout — which is the note that invites someone to narrow the walk later. Fixed, with the fixture moved and a test reading it from there, plus one that keeps a flat file working. (#3678)
- Every generated hook and plugin names the launcher, not the binary the install ran from. The launcher exists so a config never has to name a build (#3422) and the config writers have used it since; the writers that generate a plugin or an extension file kept baking the absolute path. On the machine this was found, eleven auto wirings — opencode, kimi, antigravity, pi, senpi, hermes, OpenClaw, cline, omp, DeepSeek Harness, Goose — ran hooks through builds in a scratch directory, and the day that directory is cleaned auto-recall stops there with nothing said. Nine writers changed; Goose was already going through it under the other helper's name, which a first pass missed and double-wrapped, pointing the launcher at itself. One test over every auto target now holds the invariant: install with a distinctive path, and no line running a `hook-` subcommand may carry it. Per hook line rather than per file, because qwen, crush and ZCode keep the MCP server in the same file and that entry names the binary deliberately. `deja install <harness>-auto` rewrites a file that already carries a baked path. (#3682)
- A hook entry from a build under another name is deja's own, so an install collapses it instead of stacking beside it. Reported as "the hook seems to fire several times", and it did: `~/.claude/settings.json` on the machine this was found carried six deja entries per event, one per throwaway build that had ever run an install — six processes per prompt and the recall block arriving several times in a turn. The same count elsewhere on that machine: codex 30 deja commands, cursor 35, qwen 32, grok 68. An entry was recognised as deja's own by the exact command string, by the basename `deja`, or by a path in a record capped at ten — and a build called `deja-cont` matches none of those, so every install read the old lines as somebody else's and added its own. It is the pair that identifies them now: a binary named like a deja build running one of deja's own hook subcommands. A wrapper line that merely contains the hook is still left alone, and a foreign `other hook-prompt` stays foreign. Six writers share the predicate, so one change collapses all of them — measured on copies of that machine's files: claude 6 → 1 per event, codex 30 → 5, cursor 35 → 5, qwen 32 → 4, grok 68 → 4. `deja doctor`'s repeat note was silent for the same reason and now fires. (#3681)
- The two rows with a database half say so when `sqlite3` is missing. Kilo Code and ZCode each keep part of their history in SQLite and named the file without naming the tool that reads it, so on a machine without the CLI those sessions were absent from recall while the row reported the store present. Every store that is only a database already said it; these two have transcripts as well and took a different path through the report. (#3679)
- ZCode's CLI database is read. It was left out because nothing said what shape it was in, and a reader built on a guess loses the half of a store it does not understand in silence. The shape is now attested by `zcode-stats` 0.8.0, a read-only dashboard over the live database: it names `~/.zcode/cli/db/db.sqlite` and queries `session(directory, task_type, …)`, `message(id, session_id, data)` with `json_extract(data,'$.role')`, and `part(data)` with `json_extract(data,'$.type')`. That is OpenCode's schema, which deja already parses for OpenCode and for Kilo's CLI — so this is one more root for an existing reader, with `DEJA_ZCODE_DB` to move it and `sqlite3` as the prereq for that half. The entry says what this rests on: a third party's attestation, not a running ZCode checked here. (#3675)
- The Cherry Studio import file is pinned against the app's own validator, read out of the installed bundle rather than from its repository: `object({ mcpServers: record(string(), McpServerConfigSchema) })`, where the server schema is `.strict()` and `type` is one of four literals. What deja writes validates — and the strictness is the reason to pin it, because one key the app does not know fails a paste whole instead of being ignored. The server also arrives named `deja`, since the importer takes the name from the key. (#3674)
- `deja resume` prints `kilo -s <id>` for a Kilo CLI session instead of refusing it. The rule told the extension's tasks from the CLI's sessions by comparing the session's path with the database file — and a session read out of that database carries the directory it ran in, not the file, so every CLI session was refused as an editor task. The one half `kilo -s` exists for. It tells them apart by the task path now and runs the command in the session's own directory, the way the opencode case does with the same field. Verified by running what deja prints: Kilo reopens the session and its screen shows the original turn. (#3677)
- `deja install kilocode` wires Kilo's CLI, which is a different program from its editor extension and has its own config. Kilo vendors OpenCode, so `<config>/kilo/kilo.jsonc` takes OpenCode's `mcp` block; deja wrote only the extension's settings, so on a CLI-only machine the install said "only the skill was written" and `kilo mcp list` said "No MCP servers configured". Both are written now — `kilo mcp list` prints `✓ deja connected` — the note names what was skipped rather than what was not, and the doctor row points at whichever of the two configs is on the machine. Verified on `@kilocode/cli` 7.7.3, along with the CLI store: a real `kilo run` session landed in `~/.local/share/kilo/kilo.db`, deja indexed it and recall returned the typed phrase. (#3672)
- Senpi is wired, not just read, and the five capabilities the registry had as `unknown` are answered. They were unknown for one reason — nobody had found a package for it — and there is one: installing `@code-yeongyu/senpi` answers every surface on senpi's own screen. The server goes in `<agent>/mcp.json` (its palette lists `mcp:deja:deja`), the shared skill is what it reads (with both copies present it prints `"deja-history" collision: ✓ <agent>/skills ✗ ~/.agents/skills (skipped)`, so the local one is retired on install), pi's extension loads unchanged and brings the `/deja` command, and a session started with it records what `deja hook-context` returned as `{"type":"custom_message","customType":"deja-recall"}` — auto-recall arriving, in senpi's own transcript. `deja resume` prints `senpi --session <id>` and `deja handoff --to senpi` runs. One thing to know before wiring both clients: senpi's first run moves `~/.pi/agent` to `~/.senpi/agent`, sessions and config and extensions together, so pi is left with an empty directory. (#3670)
- `deja doctor` stops counting an extension's own state as transcripts it failed to read. Senpi's terminal extension writes one file per session under `sessions/<project>/extensions/terminal/`, and the row said "2 not recognised here" about a store whose every transcript had just been indexed — while `doctor --json` for the same store said `ok` with no unread count at all. Anything under an `extensions` directory inside a session root is the harness's state, not a conversation; the same call Continue's `sessions.json` and Kimi's `state.json` already get. Reachable for every pi descendant. (#3669)
- `deja doctor` says whether `/deja` is installed. MCP wiring has a section and hooks have one; the command — the third thing an install writes, and the one that makes deja discoverable when someone types a slash — had none, which is why Gemini renaming it to `/user.deja` had to be found on Gemini's own screen rather than by any check deja ships. Eleven rows, name and state and path, and the path is worth reading: two harnesses keep the command under a different name. `someone else's` is its own state, separate from `missing`, because only that one is a reason to leave a file alone. Also a `commands` array in `doctor --json`, from the same table, so the two surfaces cannot disagree. (#3664)
- Command Code is wired, all four of its surfaces: the server in `~/.commandcode/mcp.json`, a skill, a `/deja` command, and hooks under a `hooks` key in `settings.json` — `deja install commandcode-auto` for the last. Two details there would fail silently if copied from another harness and are pinned by tests: its timeout unit is seconds, so the milliseconds deja passes elsewhere would read as ten minutes, past its documented maximum; and its tool matcher is a regex over its own display names (`SHELL`, `EDIT`, `WRITE`), so a Claude-shaped `Bash` matcher never fires. There is no per-prompt event — the four are SessionStart, PreToolUse, PostToolUse and Stop — so the digest rides SessionStart. The CLI is closed and none of this is verified here: every path comes from two independent integrations that cite the vendor's docs, and the registry entry says exactly that. (#3651)
- `deja install kiro` writes Kiro's global steering file, which is its user-level guidance channel: `~/.kiro/steering/deja.md`, scanned for every project. Four lines, and the length is the point — a steering document declares an inclusion mode and the only one measured as actually loaded by kiro-cli is `always`, so this text is in front of every turn whether it is wanted or not. `manual` is not loaded and cannot be invoked from a session, which is also why the registry records Kiro's skill as impossible rather than as work: an on-demand skill is not something Kiro has. (#3651)
- `deja resume` prints `kilo -s <id>` for a Kilo Code session that came from its CLI store — the flag is "session id to continue" in Kilo's own CLI options. An editor task is refused with the reason instead: those live under the host's globalStorage and reopen from Kilo's history view, which is the same split Roo has and the reader already tells apart by path. (#3651)
- Cherry Studio gets the skill, and the gap that said it had no channel for one was wrong: it discovers the skill directories of the agent CLIs a machine has — `~/.agents/skills` among them, which is deja's own shared channel — and lists what it finds for the user to enable per agent. `deja install cherrystudio` writes the file there, so the app lists it; enabling it for the agent is the click left, and the install note says so. (#3651)
- `deja resume` works for Continue, and the registry entry that said it could not was wrong about Continue's own CLI: `cn --fork <sessionId>` loads any session in the store deja reads — its history manager opens `<sessions>/<id>.json` — rather than only the last one. The flag forks, so the history comes back under a new id instead of continuing the old one, and that is printed on stderr beside the command rather than left for the user to discover. (#3651)
- `deja resume` prints a real command for Kiro, Kimchi and gajae-code instead of handing over a paste. Both were recorded as unverified; both are settled from their own sources — Kimchi's argument parser rewrites `--resume <selector>` to `--session <id>`, and gjc's session-operations document has `--resume <id|path>` opening an existing session, forking it into the current project when it belongs to another, which is why deja prints no working directory there. Kiro takes `kiro-cli chat --resume-id` on 2.2.0 and newer, and a Kiro IDE session — recognisable by its `sess_` id — is refused with the reason, since those reopen from the app. (#3651)
- Kilo Code gets a `/deja` command. The registry had it down as work nobody had found the surface for, and Kilo's own workflows doc names it: global slash commands are markdown files in `~/.config/kilo/commands/`, so a file called `deja.md` there is invoked as `/deja`. (#3651)
- VS Code Copilot Chat gets a `/deja` command, which is the only surface it has: it fires no session-start and no per-prompt hook, so before this the tool was there and nothing told the model to reach for it. Its commands are prompt files, so `deja install vscode` now writes `<User>/prompts/deja.prompt.md` for every host it finds — the same `User` directory the reader already walks — with the description the chat box lists, an argument hint, and the CLI as the fallback for a window where the tool is not connected. (#3651)
- ZCode gets both halves: `deja install zcode` writes the server into `mcp.servers` in `~/.zcode/cli/config.json`, and `deja install zcode-auto` adds hooks on SessionStart and UserPromptSubmit to the same file — so recall arrives without being asked for. Three details decide whether that works and every one of them fails silently: config-file hooks do nothing without `hooks.enabled`, a config hook gets no template expansion so the command needs an absolute path, and the output schema is strict — one unrecognised key and the whole response is discarded. That last one is what the new `--strict` flag on `deja hook-context` and `deja hook-prompt` is for: it drops deja's receipt line, which no other host minds, and keeps the context. Shapes from the memory plugin that inspected a live ZCode and shipped an installer against it, not from ZCode's own docs, which do not describe them. (#3651)
- Three guide pages for the harnesses that just landed: Kiro, Kilo Code and Cherry Studio. Each answers the question people actually type — where that tool keeps its history, whether it remembers anything between sessions, how recall gets in — and each carries the one detail its store has that a reader would trip over: Kiro's reply arriving in pieces under one message id, Kilo's two stores and the migration between them, Cherry Studio's snapshot per stream chunk and the import its MCP list needs. Twenty-two per-agent pages became twenty-five. (#3103, #3644)
- `deja install kiro`, `deja install kimchi` and `deja install gjc` wire three of the six stores that landed read-only, each path taken from that tool's own source rather than from the lineage it belongs to. Kiro's server goes in `~/.kiro/settings/mcp.json`, which both its CLI and its IDE read, with a note about custom agents not inheriting global servers. Kimchi's goes in `<agent dir>/mcp.json`, honouring `KIMCHI_CODING_AGENT_DIR` the way the reader does. gjc gets the server and its native skill directory — the one gjc actually loads, since Claude's and Codex's are import candidates there. What is still somebody else's switch is recorded as such: Kimchi runs deja's Claude hooks and skill only after two compatibility extensions are enabled by hand, and gjc's hooks are TypeScript modules with pi's event names, which is a port rather than a config line. (#3651)
- `deja install kilocode` wires Kilo Code rather than only reading it: the MCP server into `<globalStorage>/kilocode.kilo-code/settings/mcp_settings.json` for every host that carries the extension — the file Kilo's own `KilocodePaths` names — and the shared manual into `~/.kilocode/skills/deja-search/SKILL.md`, where its loader looks first. A machine with the CLI and no editor gets the skill and a note saying the server was not wired anywhere. Hooks it does not have, so auto-recall stays a gap pointing at the upstream pull request rather than a promise. (#3643)
- `deja install cherrystudio` hands the app a server it can import. Cherry Studio keeps its MCP servers in its own SQLite store — a drizzle schema seeded from a built-in preset list — so there is no config file to write, and writing into a running app's database is not an installer's job. What it has is Settings → MCP → Import from JSON, so deja writes `<config>/deja/cherrystudio-mcp.json` and says where to point it, the way the aider target writes a file the tool will not fetch itself. The note rides on a second run too: the file being unchanged does not mean anyone has imported it. Hooks and slash commands the app has no third-party surface for, and the registry records that as impossible rather than as work someone could pick up. (#3644)
- Five more stores are read, and none of them needed a new format. Senpi and Kimchi Coding are pi descendants that kept its envelope, so both are a root and a name; Command Code and ZCode write a flat `role`/`content`/`timestamp` transcript under a Claude-shaped project directory, which is one small reader for the two. Two details are worth the words: Command Code keeps `<session>.checkpoints.jsonl` beside each transcript, a snapshot stream that read as a conversation adds a session with no words in it, so it is skipped by name; and a line whose role is neither user nor assistant is tool output, which is where a command's error text lives and where a user's next search for it starts. ZCode's SQLite store stays unread until a sample of that schema exists — a reader built on a guess drops the half of a store it does not understand without saying so. gajae-code is a fifth of the same kind, and it keeps its sub-agent passes one directory below the session they belong to — indexed as sessions of their own they repeat the parent's work and compete with it for the same recall slot, so they are skipped unless DEJA_INCLUDE_SUBAGENTS=1 asks for them, the switch Claude Code's and Cursor's sub-agents already use. Shapes read from tokscale's readers, which is the only place they are written down. (#3647)
- Kiro is read, both of its clients. The CLI writes a header and a transcript per session under `~/.kiro/sessions/cli`, and its records carry the text in `data.content[].data` with the time in seconds — with one thing worth knowing: several `AssistantMessage` records can share a `data.message_id`, because the client appends the answer as it streams and each record holds the next piece rather than the whole answer so far, so a run under one id is joined and a recall quotes a sentence instead of a third of one. The IDE — Kiro is a VS Code fork — writes a directory per session under the workspace instead, `session.json` beside `messages.jsonl`, in a `payload` shape current builds use and the flat `role`/`content` shape they used before; both are read. Its own bookkeeping records are not turns and are dropped rather than attributed to a role. Read support only for now, and two stores are deliberately left unread: the IDE's globalStorage mirror and the TUI's `conversations_v2` SQLite, where no sample is in hand and a reader built on a guess is one that loses history quietly. (#3103)
- Cherry Studio is read. It runs Claude Code sessions from a desktop app and writes them as ordinary Claude Code transcripts under its own app data, so the parsing is Claude's — with one difference that matters for text rather than for tokens: Cherry Studio appends the same API call three or four times as the response streams, a new uuid each time and the text growing. Read plainly that is one reply stored three times in prefixes, so a recall could quote half a sentence and `deja show` print the answer twice before finishing it; the reader collapses a run by its request id and keeps the longest. Stock Claude Code writes one record per turn and its reader is unchanged, which a test pins. 51,851 stars, and the store was unread. (#3644)
- Kilo Code is read, both of its stores, and neither needed a new parser. The extension (`kilocode.kilo-code`) writes Roo's task shape under the host's globalStorage, and the CLI writes OpenCode's message schema to `~/.local/share/kilo/kilo.db` — Kilo is a Roo fork that vendors OpenCode, and its own `legacy-migration` reads the task directory to import it into that database, so the task files are the history of anyone who used it before the migration and the database is where it goes afterwards. 27,321 stars and deja read neither. Read support only for now: nothing is wired into Kilo, which the registry records as a gap rather than leaving blank. (#3643)

### Fixed
- The numbers the repository states about itself match the runs they came from. The contributing guide quoted 31 merged pull requests of 35 from outside, median four hours: over all 50 it is 45 merged, median 3.5 hours, slowest two days. `SECURITY.md` named two network paths where the security model has always listed four, and the privacy page's own "local by default" list left out `deja embed` six paragraphs above explaining it. The 85.3% hit@1 in the README, on the site and in `llms.txt` now names the 470-question cleaned set it scored on. The roadmap's line-level blame figure still said "about a quarter of lines", which was the per-commit rate — the measured per-line number is 40% of 150. (#3863, #3868)
- The README caption said every line in the demo GIF is "quoted from two real sessions"; `scripts/demo/agent.tape` says what it is, and has from the start — a genuine run, real model and real tool call, against the synthetic corpus `scripts/demo` lays down so nobody's history is published. And "every memory tool starts empty and records forward" is contradicted by our own day-zero table, where six of seven read history already on disk. (#3866)
- `deja install dsh` and `dsh-auto` resolve. The DeepSeek Harness guide prints `deja install dsh-auto` and the binary refused it — the targets were `deepseek` and `deepseek-auto` — so anyone following that page got `unknown target`. Both names work now and take the same guidance file, and a test walks the docs for every `deja install <target>` they print and fails on one the binary refuses. (#3869)
- Example hosts and addresses in the fixtures come from the documentation ranges. Peer, friction and install tests carried private-range addresses and a maintainer's first name, including in two production comments. (#3861)
- `deja doctor` can read the server entry under Zed's own key. Its settings name the server `deja-context-server` — the id the extension owns — in a `context_servers` map, in a file with comments in it, so the JSON walk never ran, the container key was one nobody had added, and the fallback looked for a key called exactly `deja`. The row said `wired` while the server pointed at a build in a scratch directory. A quoted key whose name starts with `deja` now counts as deja's own in the files read as text, `context_servers` joins the containers the JSON branch walks, and a file that parses but holds nothing of deja's under any known container falls through to the text reader rather than answering "nothing". (#3683)
- `deja doctor` can name the binary codex runs. The keyed read walked forward from the anchor and stopped at the first sibling that was not `command`, which describes a YAML mapping and not a TOML table: every key of a table sits at the header's own indent, so the scan broke on the line codex writes right after `[mcp_servers.deja]` — `type = "stdio"`. A config whose command was not the table's first key therefore read as naming no binary, and neither the gone-binary check nor the temporary-directory one could fire. Found by `codex mcp list` naming a build in a scratch directory while the row said `wired`. (#3668)
- `deja doctor` can name the binary OpenClaw and ZCode run. Both keep their servers one level deeper than everyone else, under `mcp.servers`, and that map was read as though it were a single entry — no command in it, so the walk returned nothing and the file parsed as JSON, which meant the text fallback never ran either. Neither the "the binary is gone" check nor the "the binary is in a temporary directory" one could fire for those two; on the machine this was found, OpenClaw's entry ran a build under a scratch directory and the row said `wired`. (#3663)
- `deja doctor` reads the binary a harness is wired to from the entry deja wrote, not from the first `command` anywhere in the file. goose keeps its `slash_commands` in the same `config.yaml` as its MCP extensions and one of those commands is called `deja`, so the whole-file scan answered with that name — a bare word, which is on PATH and is not an absolute path, so both binary checks had nothing to look at — while the extension three lines from the top ran a build left in a scratch directory. The attributed read goes first now. The class is wider than goose: any config that names deja twice, once as a server and once as a command or a hook, could answer with the wrong half. (#3662)
- `deja doctor` reports the MCP wiring of all nine harnesses it never named: DeepSeek Harness, Roo, Kilo Code, Kiro, Kimchi, gajae-code, ZCode, Command Code and Cherry Studio. The table those rows come from also carries every per-row check — duplicate registrations, a binary that is gone, a binary in a directory something else will delete — so for those nine the report had no state, no path and no check. On the machine this was found every row that had a check was repaired and dsh was still running a build from a scratch directory, with Roo pointing at another one. Two readers had to learn a shape for the rows to mean anything: dsh has no server key at all — `serverName: deja` is a field in a patch-list row with `command` beside it, not below — and four harnesses' manuals are written by their own install target rather than by the generic guidance step, so that column said `unsupported` about files deja had written. (#3661)
- The wiring repair no longer adopts a build in a directory something else will delete. It stood down for the machine's TMPDIR and nothing else, so a scratch build under `~/.claude/jobs/<id>/tmp/` counted as "deja moved" and rewrote every recorded target to itself — on the machine this was found, 28 of them, which is why repairs made by hand kept coming back stale. The rule is now one directory called `tmp` or `temp` anywhere in the path, shared with the check that names a disposable binary in `deja doctor`. The only screen that ever said this was happening was Gemini's own start banner, quoting deja's note back. (#3656)
- `deja doctor` stops calling Zed wired when nothing can start. Zed takes the server in two shapes under one id: the entry `deja install zed` writes, which names the binary, and the entry Zed writes when an extension provides it, which names nothing and defers to `extensions/installed/deja-context-server`. On the machine this was found, that extension was a symlink into a scratch directory that no longer existed — an enabled server with no executable path anywhere — and the row said `wired` because the id was in the file. It now says the entry expects an extension that is not installed — and `deja install zed` repairs that entry instead of reporting "unchanged", which is what it did before: refusing to touch an extension-shaped entry is right while the extension is there and wrong once it is gone, and the remedy the report named did nothing. (#3660)
- An MCP entry that runs a deja build under another name is visible again. `go build -o /tmp/deja-probe` and an install from it leave an entry no later deja recognised: the name test knows `deja`, `deja.exe` and `deja-hook`, so `deja-cont` matched nothing, both binary checks stayed silent, and the row said `wired` because the server key was right. The key is the claim now — an entry under the `deja` key is deja's whatever binary it names — read for JSON, and for the YAML and TOML spellings the formats that are parsed as text use. Found on a machine whose Hermes server pointed at exactly such a build. (#3659)
- `/deja` works in Gemini CLI again. Its command namespace is flat and it lists the MCP server's own prompt beside the command files, and deja named both of them `deja` — so Gemini renamed both, to `/user.deja` and `/deja.deja`, and the name the install receipt tells people to type belonged to nothing. The file is `deja-search.toml` now, invoked as `/deja-search`, and the colliding one an earlier version wrote is removed on install. Found by taking a screen of Gemini's own first page: `gemini mcp list` reports the server as connected either way. (#3655)
- `deja doctor` stops calling codex's hooks wired when codex will not run them. Codex pins trust per hook, not per file, so a machine that approved deja's hook when there was one sits with four unapproved — its own first screen says "5 hooks are new or changed… Continue without trusting (hooks won't run)" while the row said `wired`, because the row was about the file. Measured here: five events in `hooks.json`, one pin in `config.toml`. The row now says `1 of 5 hooks approved` and how to fix it. (#3654)
- pi no longer announces a deja skill collision on every start. deja was writing the same skill twice into one host — pi's own directory and the shared `~/.agents/skills`, which pi also scans — so pi loaded one, skipped the other and printed the conflict every time. One file now, and the old copy goes with the install. (#3657)
- `deja doctor` says when a harness is wired to a deja that is neither this binary nor the one on PATH. On the machine this was found, grok's server pointed at a build left behind by a probe run in a scratch directory: it works until that file goes, and the row read `wired` because the config file was fine. The note names the path and the install target; deja's own launcher shim and the PATH binary are not strangers, which is what keeps it quiet on an ordinary machine. The MCP rows carry it too, and that is where it earned its keep: five harnesses on that machine — grok, gemini, antigravity, goose and omp — were pointing at builds left behind by probe runs, every one of them reported `wired`. (#3656)
- The two remaining git budgets on the hook path get the same treatment windows already got for the root lookup. The compaction fingerprint runs seven git calls under one deadline, and when it expires the result is not an error a user sees but the opposite: the fingerprint comes back `partial:` and tells the agent not to trust the context deja has just handed it — on the runner one call spent all 750 ms on a repository with a single dirty file. The file-ranking probe has the same shape, and what it costs when it misses is the ranking itself: recall falls back to recency silently. Both are platform budgets now, and both have a test on the number the product ships rather than on a number the test widened to keep itself quiet. (#3645)
- goose sessions on macOS are read. goose resolves its own directories through etcetera's `choose_app_strategy` with author "Block" — the Apple strategy on a mac — so its store is `~/Library/Application Support/Block/goose`, and deja looked in `~/.local/share/goose` on every platform: a mac user who had used goose was told `goose missing (0 files)`, which reads as never having used it. Every candidate root that exists is now read rather than the first, so an install predating the change keeps working, and `deja doctor` and `deja sources` name each directory they looked in. The machine this was written on has the old layout, which is why a single root looked right from inside it. (#3642)
- A Codex rollout older than seven days is read again. Codex compresses one to `rollout-*.jsonl.zst` after that (`COMPRESSED_SUFFIX`, `MIN_ROLLOUT_AGE` in its own `rollout/src/compression.rs`, run by a background worker) and reads either name itself, while deja wanted `.jsonl` alone — so on a machine where that worker had run, every session older than a week left the index without a word: a file that is not a candidate is not a skip either. Reading one needs the `zstd` CLI, and a store that holds compressed rollouts without it now says `zstd CLI not found` the way the DeepSeek Harness and Zed stores already do. `archived_sessions/` is read beside `sessions/` — Codex's own second directory, same JSONL — and the same session under both names is read once, plain copy first, because Codex materializes a rollout back before appending to it. Index format 48. (#3640)
- A value whose label says it is public stays readable. The entropy pass takes the word before the separator as the label, so a WireGuard dump — `public key: <base64>`, once per interface and once per peer — read as `key:` and every one of those lines was stored as `[redacted:entropy]`. On a 2,719-session store that was the largest single class inside the entropy tier, which is half of all redaction, and none of it is a credential: a published key masked in the index is a line no question about it can match. `peer:`, `api key:`, `secret key =`, `private key:` and a bare `key:` are unchanged, and `api_key=` was never the entropy pass's to begin with. Index format 47 — a store built before it keeps the masked text until it re-reads its sources. (#3638)
- The repository-root lookup gets a budget windows can meet. `git worktree list` is 5 ms where processes are cheap and the 400 ms bound was sized for that; a cold `git.exe` misses it, and what a user loses is the project scoping for an agent started in a subdirectory — silently, since the lookup is best-effort. Two seconds on windows, unchanged elsewhere, and the test that pins the behaviour names a machine slower than the budget instead of failing on it. (#3624)
- A release says what the repository still has to catch up on. Two steps are left behind by design — the Windows manifests are attached to the release rather than committed, and each extension's version is decided by npm and written into a temporary copy — and on 15 September both were forgotten after 0.20.1: the manifests turned every later pull request red, which is how anyone found out. The reminder now arrives with the release, as one issue that is commented rather than reopened, carrying the two commands. (#3627)
- Each published extension's version in the repository is what npm serves. The release writes the number into a temporary copy, so the files here never learned it: `extensions/opencode/package.json` said 0.1.2 for a package npm serves as 0.20.1, and all four were behind. The guard that should have said so compared npm against the newest release tag — a rule that stopped being true when a release ahead of npm started publishing the next patch of that package's own line rather than skipping it (#2993), and one a shallow CI checkout could never evaluate, since it has no tags. It compares the repository against npm now, and `node scripts/extension-drift.mjs --write` is the catch-up. (#3627)
- `deja forget --session` and `--project` exit non-zero when the selector names nothing. `deja forget --session $ID && echo removed` printed "removed" for a session still on disk under another id — a script reads the exit code, not the wording, and the sibling `--unforget` has refused a miss since #2263. A window is not a name: `--before 30d` on a young store still exits 0. (#3601)

## [0.20.1] - 2026-09-15

### Changed
- The relevance tier reads and folds the messages that matched instead of every message of every candidate. The ranking already knew which records they were — the per-message signals are keyed by their offsets — and the exact tier has read records at offsets since it was written. On a 2,716-session store one relevance query was folding 120.7 MB across 140,355 messages, of which 10.3% held any query term: measured interleaved, 1,740 ms to 480 ms and peak RSS 377 MB to 158 MB. Quality is unchanged on every benchmark — LoCoMo 69.7% hit@1 / 0.767 MRR, LongMemEval 84.8% / 0.894, day0bench over 19,195 sessions 13/60 hit@1 and 27/60 hit@5 with p50 80 ms to 61 ms, and the recall and prompt benches identical. (#3491)
- A search hit carries the passages that matched rather than its session's whole transcript: the matched messages, each with the answer after it, at most twenty per hit, plus `messages_total` and `messages_capped`. The size of an answer used to be the size of the reader's longest transcript — on a 2,716-session store `deja search --json` returned 136 MB over 50 hits and 140,841 messages, and encoding it was a second of the 2.7 it took and half a gigabyte of resident memory. Measured interleaved on that store: 136.18 MB to 2.60 MB, 2,660 ms to 1,790 ms, peak RSS 803 MB to 361 MB, with the recall, context and prompt benches unmoved. `deja blame --json` takes the same bound. (#3620)

### Added
- A store can be excluded, not just a project: a line prefixed `harness:` in `~/.config/deja/exclude`, or `DEJA_EXCLUDE_HARNESSES`. deja then neither walks it nor asks for the tool that would read it, and every screen that mentions stores says so — `deja doctor` on both its surfaces, `deja sources`, and the empty screens, which used to blame a machine no agent had run on for a store that is on disk and deliberately unread. Previously the only way to stop `needs-sqlite3` advice for a harness the reader does not use was to install the package. (#3499)
- `deja bench read`: what it costs to read a database-backed store, and what one long escape-heavy value does to it. Every other benchmark runs against an already-indexed corpus, which is how a reader that took 2,287s on a 6.16 MB value stayed invisible while all of them held flat. (#3552)
- A store that is slow to read says which one it is and that it is still moving, every thirty seconds, and its read time lands on its line. A pass over a 520 MB store gave thirteen minutes of one static line and no way to tell a slow read from a stuck one; the slowest store on a 3.4 GB corpus reads in 10s, so nothing says anything on an ordinary run. (#3555)
- `deja bench ingest`: what an index update costs, per class of change — unchanged, an appended turn, a new transcript, a renamed one, a rewritten one — with whether the pass replaced the records already on file as the gate. (#3507, #3546)

### Changed
- CI gates what an update costs, per class of change. `deja bench ingest` has reported it since #3507 and nothing in the build looked, which is the gap that let a path re-tokenise the whole store on every new session for two months and twenty releases (#3500). The gate is the shape rather than the clock: the five classes have to be there and only a rewritten transcript may make the pass replace the records already on file. Verified by putting #3500's shape back — the check fails on it. A wall-clock margin was tried and dropped: locally it was five-fold, on the runner `new transcript` came in 1% above the rewrite. (#3505)
- The nightly migration matrix checks older config shapes as well as older stores: each release writes its own wiring, and the build under test has to read it as wiring rather than as nothing, and has to call it dead once what its entries name is gone. (#3505)
- `deja doctor --json` carries the auto-recall rows, not only the MCP ones: a script could see a missing sqlite3 and not a hook running a binary that is gone. (#3540)
- An upgrade says why it is re-reading every source. Every other reason for a full pass names itself; a version difference printed the line a first install prints while doing the longest piece of work deja does on a large store. (#3500)
- Index format version 41 applies the new redaction to text already stored. Redaction runs at ingest, so every earlier fix to it reached only the next conversation; a store re-reads its sources once. (#3535)
- Index format version 39: dsh names its logs `session.v3.jsonl` now, and a store that already holds the older names re-reads its sources once so the new ones join it without `deja index --rebuild`. (#3508)
- A new transcript is appended to the index instead of rewriting it. Every new conversation is a new file, and the path that refused an unseen one cost 4.76s against 0.28s on a 171 MB index, growing with the store rather than with the file. (#3503)
- A search quotes the hits it serves rather than every session that matched, so its cost follows what was asked for: on a store where every session matches, 806 ms and 127.8 MB against 745 ms and 88.9 MB. (#3544)
- The freshness walk no longer runs to be discarded: `deja index` walked every store a second time whenever it had already reported what it did, 52 ms on a 2.0 GB store. (#3501)

### Fixed
- A file sitting where the index directory belongs is left alone. The swap parked it as `<dir>.old` and then deleted it, so pointing `DEJA_INDEX_DIR` at a database or an archive by mistake removed the file, said nothing and exited 0. A build refuses and names the path now, and `doctor` says which state it is in — `index.state` gains `path-is-a-file` — because "run `deja warmup`" is advice that cannot be followed there. (#3610)
- `deja` says why a first build could not run. The screen a new install sees reported `open /…/index.db.lock: permission denied` — an internal lock file and an errno — while `deja index` in the same state names the directory to fix and the variable that moves it. (#3613)
- `deja sources` no longer reads the two stores whose rows it writes by hand. An excluded opencode kept being opened and kept printing the sqlite3 error the exclusion exists to silence, while `doctor` said `excluded` for the same store — the row the loop prints now covers aider and opencode too. (#3611)
- `deja show` no longer prints a session from a store the redaction floor withholds. Every ranked surface rebuilds before answering from a store written under redaction rules this build has moved past; `show` loads by identity, and so did the MCP tools that share that loader — which is the surface that shows the most at once. The loader refuses and names the state, and `show` re-reads the sources first, the way the ranked paths do. (#3617)
- `search --json`, `last --json`, `show --json` and `blame --json` strip the characters that make displayed order disagree with stored order. The printed surfaces have stripped U+202E and the invisible tag block since #1090, and the encoder escapes control bytes — which is why the half it does not escape went unnoticed on the one surface a dashboard reads. (#3616)
- A secret written `api-key "…"`, `api key "…"` or `x-api-key: "…"` is masked. The gate in front of the quoted-secret rule tested three spellings of the name while the rule's own pattern accepted a fourth, so which spelling a tool printed decided whether the value was stored in the clear; the colon form fell between that rule and the key-value one, whose value class starts at sixteen characters. Index format version 46 masks what is already stored. (#3614)
- The point-of-action hook reads what a command settled instead of searching for it. Finding it at the moment of the action meant ranking candidates by the command's words and then loading whole sessions to ask which of them had actually run it — 133 ms an action against 21 ms, on the surface that fires on every action, and never warm because almost every action runs a command its session has not run before. The build now stores each session's conclusion and, per command, which session ran it last, so the answer is two map lookups: measured over 42 actions on a real store, decisions 4 to 16 and the p90 call 174 ms to 41 ms. Index format version 45. (#3001, #3605)
- The Zed extension asks the host whether it can run `deja` before downloading its own copy. The check it had asks the filesystem for four absolute paths, which an extension cannot reach from inside the wasm sandbox — measured on the same target it is built for, every candidate comes back `NotFound` while a run with the filesystem preopened finds both — so a user who already had deja was handed 11 MB of it anyway. Running `deja --version` through the host answers the only question that matters, by the same mechanism Zed uses to launch the server. (#3392)
- Every read against a SQLite store carries a wall-clock budget, ten minutes by default. One sqlite3 child ran 13m54s with 0.75s of CPU in deja itself and nothing in the tree set a deadline, so `deja index` looked hung rather than slow; a store that runs out is now an ordinary read error, named by `deja doctor`, and the rest of the index still builds. For scale, the largest store here is a 3.2 GB opencode database that answers in 11.7s. `DEJA_STORE_TIMEOUT` overrides it, and a zero turns it off. (#3555)
- The pre-tool hook says the line behind a fact it has already given this session, instead of going quiet. It picked one line from four producers ordered by certainty, and when that line turned out to be a repeat the call ended there — so one absent program, named once, silenced the surface for every later command that mentioned it: three lines in ninety-one actions on a real store. The retry now walks every producer, paying lookups alone past the first two: 7.5 ms against the 111 ms the same walk costs when it searches. (#3603)
- `deja doctor --json` carries the Claude Code and codex hook rows. Both print in the text report and neither reached `auto_recall`, because both predate the table the other nineteen harnesses share — so a script watching its own install saw nothing about the harness most people run, including the state an upgrade leaves where every hook exits 127. Each row is decided once and read by both surfaces. (#3608)
- `deja doctor --json` says when the store is not what this build writes. It called a store `ok` while its recall was off, so a script watching index health saw nothing wrong — the same miss #2292 closed for damage. `index.state` gains `rereading` and `index.format` names which of the four states it is in. (#3600)
- `deja forget` calls a `remember` note what it is on the run that drops it, not only on `--dry-run`. The sentence that tells a written note from a promoted one was wired into the dry-run branch alone, so checking first and then doing it gave two different answers, and the wrong one was on the path that changes something. (#3599)
- The session-start block no longer quotes a store the redaction floor is withholding. It reads the friction sidecar rather than the records, so it went around the gate that stops text written before a redaction fix from being shown, and put a wall from the old store into the model's context on the first session after an upgrade — in the same message that said recall was not ready yet. Every other surface runs a rebuild first; the hook cannot, which is the reason the floor exists. (#3598)
- `deja doctor` no longer says the index cannot be read and then reads it. The row was driven by the content version and worded for the on-disk layout, so the common upgrade — a role filed better, a title read from a different field, which outnumber the redaction bumps three to one — was reported as a store this build could not open while `--deep` re-parsed its sessions three lines below. Four states now, four sentences. (#3597)
- A secret handed to `--passphrase`, `--secret`, `--token` or `--api-key` is masked. The flag rule covered `--password` alone, so `gpg --passphrase …` and every CLI that takes an account credential on its command line went through in the clear. The same change stops the rule eating a word of prose: `run with --password from the keychain` lost "from" and counted it as a secret found, and with a space rather than an `=` a value that reads as an ordinary word is now left alone. Index format version 44 masks what is already stored. (#3596)
- `deja search` says when a hit is stamped later than this machine's clock. It ranks on recency among other things, so the sessions whose date cannot place them are the ones it places first — and it was the one date-ordered surface that printed `Jan 1 2099` beside an answer and left the reader to work out what it meant. (#3595)
- `deja stats --role` reaches every role `deja help` documents. Three of six came back empty on a store holding those records: `tool` because stats compared the stored name `tool-output` against the documented alias, and `command`, `files` and `edit` because the index withholds work records from a caller that did not ask for them and stats asked for everything, then narrowed afterwards. It is also why `deja index` reported 18 messages where `deja stats` reported 12. (#3592)
- `deja stats` names the filter that emptied it instead of calling the index unbuilt. `--since 30d` over a store whose sessions are all older answered "nothing indexed yet — run `deja index`", which is advice for a state deja is not in; `--harness` and `--project` did the same. The usage line now also names the four filters stats has always taken. (#3591)
- The grok linearity test takes the best of three timing pairs. A ratio of two short timings measures the runner as surely as an absolute bound does: the same parse is a steady 4.0x here and read 8.6x once on the windows leg, failing a pull request that touched neither grok nor parsing. (#3590)
- An ordinary Russian word is no longer read as a key word: `включены`, `исключение`, `переключены` and `выключен` all contain `ключ`, and the pattern that allows words between the key and the colon reached across the sentence — a markdown link came back as `https:[redacted:credential]`. (#3589)
- A password assigned with `=` is masked at the length people actually choose, including the `DB_PASS=` spelling: a JDBC URL, a query string, `--from-literal=password=` and a dotenv line all kept theirs in the clear under sixteen characters. A colon keeps its older reading. (#3588)
- A secret whose value is not ASCII is redacted. Every key-value pattern ended in `[A-Za-z0-9/+=._-]{16,}`, so `пароль: БазаПароль2026` and `password: 非常に長いパスワード2026` were stored in the clear whatever the key word — the key words were widened to those languages and the value class was not. Index format version 43 masks what is already stored. (#3587)
- A search over a store deja cannot open says so instead of advising the index that just ran. The sentence for a permission wall sat inside the branch for a machine with no history at all, and a store whose files are visible counts as history — so it was unreachable in the case it was written for. (#3585)
- `deja doctor` says what an ignore rule actually hides, and names one that matches nothing. A rule is matched against the project name and the transcript's path, so the natural thing to write — the directory's absolute path — was inert while the row reported it as in force. (#3584)
- `deja install` writes the CLI skill only when it wired something. A target nobody has heard of refused, exited 1, and created `~/.agents/skills/deja-search/SKILL.md` on the way. (#3583)
- A mistyped command exits non-zero. `deja unforget x` printed the command that does exist and exited 0, so a script that ran it and checked the code was told the work had been done; a search that simply found nothing still exits 0. (#3567)
- `deja doctor` calls an antigravity store missing when its root is not on disk. The env override was handed back as given, so a variable pointing at a directory that is gone read as a store that is there. (#3568)
- `blame` tells an agent that a file has no history instead of answering `[]`. It is the tool called before an edit, and an empty array reads as a tool that failed; the note names the file and how many sessions were searched. (#3570)
- A password handed to a program as a long flag is redacted at the length people actually choose: `mysql --password=MyRootPass2026` and `app --password=Pass2026Short` reached `deja show` and `deja share` in the clear, because the key-value floor of sixteen characters applied to a flag that says what its value is. Index format version 42 masks the ones already on disk. (#3572)
- A search that lands while the first index build is running says one thing rather than two that contradict each other: there is no index to answer from yet, so it no longer claims to be serving one. (#3574)
- `deja install` refuses a TOML config that is already broken instead of splicing its block in: the JSON targets have always refused one, and an entry in a file the harness cannot load turns a missing bracket into deja's error message. (#3576)
- The last slice of `deja show` says it is the end of the session instead of offering a next slice that is past it. (#3578)
- `fix` no longer tells an agent that its error is not an error. The pair-mining heuristic was used on the caller and refused ten of twenty error lines a tool actually prints — `connection refused`, `permission denied`, `Exit code 137` among them — while the CLI took all twenty; the advice about pasting the output now rides along with the honest answer instead of replacing it. (#3580)
- A sync waits for the peer list's lock instead of writing over it after two seconds. On a box slow enough for sixteen writers not to drain in that time, two machines were dropped from the list — the lost update the lock exists to prevent. A lock left by a dead process is still taken over after thirty seconds. (#3558)
- A full rebuild keeps the sessions whose transcripts the client deleted. Claude Code cleans up after 30 days and the incremental pass held them, but a rebuild — what an index-version bump, a changed exclude list and a damaged index all run — wrote the store from the sources alone: 6 of 8 sessions gone on a measured store. `deja forget` still drops one for good. (#3529)
- The first question after an upgrade is answered from the index that is there while the sources are re-read behind it, the way a stale index is already handled. A content-version bump made it wait for the whole pass — 13m54s on a 520 MB store, where the agent that asked gave up after a minute. A layout this build cannot read, and text written before the redaction that masks it, still rebuild first. (#3552)
- The opencode reader tests the compaction flag by type instead of reading the value behind it: current opencode keeps an object of file diffs under the same key, which came to 247 MB of query output against 132 MB for the same 78,690 rows on a 3.4 GB store. (#3556)
- `deja index` no longer stalls on a store holding long tool output. Rows from a SQLite-backed harness are built by `json_object` rather than by the sqlite3 shell's `-json` mode, which is quadratic in the characters it escapes: 4 MB of quote-heavy text took it 412s against 0.04s, and a 520 MB opencode store took over ten minutes where it now takes three seconds. (#3553)
- A renamed transcript is not indexed again. Twenty renames of one 4 KB log left twenty-one copies in `records.bin` and twenty rows pointing at paths that were gone; search answered once, so nothing on any screen said the store was twenty times its content. (#3546)
- `deja log` says when a compaction capture stored nothing and why. The journal had recorded the reason since the capture was written; the screen printed the same line for a packet that was kept and one that never happened. (#3531)
- A bad numeric MCP argument names the number rather than the argument object: `{"limit":"five"}` answered "arguments must be an object", which is the half of the call that was right. (#3533)
- `doctor`'s clock row reads as a sentence — it borrowed the pronoun `deja last` uses, which carries its own verb. (#3527)
- A password handed to a program as an argument is redacted: `sshpass -p`, `curl -u user:pass`, `docker login -p`, `redis-cli -a`, a `.netrc` line, a `Cookie:` header, and a Russian key word with words between it and its colon. Eight of twenty-four planted shapes were stored in the clear and reached `deja show`, `deja recall` and `deja sync export`. (#3535)
- DeepSeek Harness: the v3 session logs are read by discovery and by the incremental index, where matching only one of the two left a new log unsearchable until the next rebuild. (#3508)
- DeepSeek Harness: the auto-recall plugin asks about the session's workspace rather than the directory dsh was launched from, and the generated plugins load under a home directory whose `package.json` declares CommonJS. (#3509)
- VS Code Copilot Chat: an edited file's URI is percent-decoded, and the older snapshot shape no longer fails to parse — a path with a space landed in the files record encoded, and a v1 working set dropped the record entirely. (#3498)
- `deja doctor` names a hook binary that is gone under every row state, not only a healthy one, and a bare `deja` says it once a day: after a package upgrade every entry points at the old path and the hooks exit 127. (#3510, #3511)
- Zed: the extension cannot reach an installed deja from inside the wasm sandbox, so the install instructions say to name it with the `binary` setting instead of promising it is found. (#3513)
## [0.20.0] - 2026-09-12

The release where every line deja gives an agent was read off a real store
rather than a fixture, and the same question asked of each one: is this about
what was asked. The answers were bad. 42% of the conclusions in a recall answer
were about work the query never mentioned; of 63 lines calling something a
file's prior decision, none named the file or its package; a third of excerpts
opened inside a word and nearly two thirds closed inside one; the mark for an
abandoned approach sat on million-word sessions where something was dropped
somewhere by definition. The other half of the release is the compaction. A
summary keeps the conclusions and drops the task, the files and the commands
they rest on, which costs a median of 28 actions before the next edit against 10
from a cold start — deja now captures the working context as the compaction
starts and hands it back once, with what each command did and what was left
open. Rebuilds take 30s instead of 51, a recall answer 0.9s instead of 2.3, and
`deja how` 96ms instead of 286.

### Added
- Working context survives a compaction. The pre-compact hook stores the task, the decision, the files, the commands and what is left open; the next session-start, prompt or tool call gets it back once, within a 4 KB budget, with the commands marked passed or failed and a line saying whether the repository moved since. (#3436, #3490)
- `deja wip`: what the last session in this directory was doing, derived from the transcript rather than from a note someone remembered to write. (#3454)
- A program this machine does not have is named before the command runs, with the same command that ran without the wrapper beside it — nine of the ten sessions told about a missing command at session start ran it anyway. (#3449, #3487, #3488)
- Xcode Coding Assistant sessions are indexed under the provider that ran them, Claude Code or Codex. (#3467)
- `deja friction` recognises the walls an agent actually hits: a shell's position marker is itself the error signal, and timeouts, the harness's own tool errors and the Python traceback tails join the phrase list. (#3446)
- The point-of-action hooks record the text they injected, so what deja said before an edit or a command can be read back and audited. 507 injections on a real machine carried none of it. (#3455)
- The recall benchmark asks in Russian too: 100 queries, half Cyrillic, one session per query, with recall@1 pinned at 1.00 as the regression gate. (#3481, #3482, #3483)

### Changed
- Index format version 38, on-disk layout 2. A record's role moved out of its compressed body, which is the layout change; 36 and 37 changed what deja derives from a transcript and left every byte's meaning alone. (#3446, #3458, #3464)
- A store whose content rules are stale still answers, instead of telling the agent to ask again later. 8 of 56 recalls an agent really made on a real machine came back during a rebuild, and an agent does not ask again; three of the last four bumps changed no layout. (#3472)
- A rebuild takes 30s instead of 51: the four sidecar passes run together, fix mining and the neighbour map run per session on all cores, and the multilingual redaction alternation only runs on text holding its own words. (#3462, #3463, #3465)
- A recall answer takes 0.9s instead of 2.3 and repeats 219 fewer bytes: ranking and hit-building run on all cores, the ranker stops building every message's token set to test five words, and the block explains itself once per session. (#3461)
- `deja how` takes 96ms instead of 286ms on a 2,721-session store, because scanning for one kind of record no longer inflates all 280,000 of them. (#3464)
- The session-start block leads with what was settled instead of with the newest sessions: it served six, four of which had settled nothing. Context-bench coverage 0.00 to 0.50. (#3468)
- The recall answer line picks the session's outcome, not its first reply, and reads decisions in either language — 59 of 60 decision lines on a Russian store went unseen. (#3473)
- The conclusions a recall answer lists are the ones about the query; an unrelated conclusion is offered as such rather than under a label claiming it answered the question. (#3476, #3484)
- The files line names the files the question is about and keeps them relative to the repository, where one stray `/private/tmp` path used to make all four absolute. Ten real lines: 2258 bytes to 1808, and words of the question present in 4 lines instead of 1. (#3479, #3480)
- A `deja blame` row names a few touched files instead of forty, and quotes a piece of evidence once across forks of the same session. Six real paths went from 20 sessions to 38 at the same byte cost. (#3456, #3469)
- Excerpts and the width cut end on whole words: of 400 real messages, 35% of excerpts opened inside a word and 62% closed inside one, now 2% and 30%; lines shortened to the terminal width split the last word in 56% of cases, now 11%. (#3475, #3477)
- `deja fix` answers a failing test with an edit's worth of evidence, not a command that merely followed it, and a repaired remedy carries the failing command it corrects. The repair rule compares programs instead of the shell prompt a harness stored, so a missing `timeout` wrapper is transparent. (#3458)
- The prompt and recall benchmarks refuse to measure a corpus an ignore rule hides, rather than printing zeros: run from a tree under `~/.claude/jobs` the prompt bench read 0 of 13 questions and 13 of 13 beside it. (#3460)
- The off-topic benchmark arms ask through the hook, so a gate at the hook level is measured by them. (#3485)

### Fixed
- The pre-tool block no longer claims a file has a prior decision when the line is about something else: none of 63 such lines on a real store named the file or its package, a review agent's hold on another change was offered months later as that file's position, a promoted note about the repository description arrived in front of `main.go`, and one incident diagnosis was offered for five different `main.go` — two files of one name are two files now, where one name in five pooled several real paths. (#3451)
- A standing decision is one fact rather than one line per file, and a sentence already seen is not news under another session's id: replaying a real session's 2,805 edits, the agent received 17 lines where 3 were distinct. (#3451)
- An announcement of intent is not a decision: 13% of the lines read as decisions were plans, and a block leading with one reads as having no decision at all. (#3470)
- The weak-match pointer names what was asked about instead of the session's title, where "how is the block" came back as an unrelated task note. (#3471)
- The mark for an abandoned approach is dropped on sessions too long to scan — 23 of 60 search hits and 32 marks across sixteen recall answers were on million-word sessions. (#3474, #3478)
- A quoted excerpt is what someone said, not what the harness or deja said: over twelve queries, 6% of 77 quoted lines were a teammate-message envelope and 6% deja's own credit sentence coming round again. (#3457)
- A compaction summary does not fill `recall_context`: it names everything a session did, so it matched any question and was 95% of three of eight answers, 44% of the bytes served. (#3459)
- The command warning names the project the failure happened in, so a command shape that travels between checkouts stops reporting another repository's test failure as this one's. (#3486)
- VS Code Copilot Chat: a turn the editor raised itself is not the person's words. A background-completion notification carried a command's stdout under the user role, and a confirmation click was indexed as a typed sentence; the turn still times the session and its work records are unchanged. (#3452)

## [0.19.5] - 2026-09-10

The release where deja stopped reading the harness's own text as the person's
words. Two dozen parsers were checked against real stores and every one of them
was quoting something back that nobody typed — an environment block, a skill
body, a plan approval, an editor's summary of a thread, deja's own recall coming
round again. The same pass gave the work records the harnesses already had:
what a tool ran, what it returned, which file an edit touched. Wiring is the
other half: a config now names a launcher instead of a build, only the installed
binary repairs it, and a settings file that had collected eight deja hooks per
event collapses back to one.

### Added
- Continue: its sessions are indexed — the twenty-fourth harness — and `deja install continue` writes the MCP server, the shared skill and a slash command. (#3149, #3151)
- Crush: its history is read and recall is wired into it. (#3158)
- prime-agent: the MCP server, an extension, the skill and a command. (#3125)
- Claude Code: a subagent run is indexed as its task and its answer, cc-mirror variants and headless transcripts are read, and the store keeps working under its new name. (#3141, #3146)
- OpenClaw: what a reset or a delete left behind is still searchable, the digest moved to the phase hook that fires once a turn, and a compaction forgets what it threw away. (#3123, #3142)
- Roo Code: `deja resume` reopens CLI tasks, and Roo stops asking before every recall. (#3130)
- `deja files --json`: the ranked rows a caller had to parse out of padded columns. (#3106)
- Recall excerpts carry the session's last word on the query, and say when the session came back to it after the quoted line. (#3140, #3150)
- `deja stats` counts the replies that used a recall and did not say so — the gap between memory served and memory credited. (#3144)
- Install says where deja lives and where the docs and the issue tracker are, once per index rather than on every run; a package or plugin install that printed no proof gets the same line in its first week note. (#3143, #3159, #3160)
- Hooks run through `~/.config/deja/bin/deja-hook`, a launcher that resolves the binary when the hook fires: `DEJA_BIN`, the path the install ran from, the `PATH`, then the usual install locations. An upgrade that moves deja rewrites one file instead of twenty-eight. MCP entries keep the binary path — the client spawns those without a shell — and Windows keeps it too, because a `.cmd` cannot be exec'd like a shebang script. (#3426)
- `deja doctor` counts deja's entries per event, reports a session served the same injection twice inside a second, and says when the launcher has no deja to run. (#3424, #3428, #3434)

### Changed
- Index format version 35: a store re-reads its sources once, so the parser fixes below reach histories that were already indexed — titles included, which is how the sessions named by a compaction summary get their names back. (#3290, #3439)
- `--limit` binds the ranked and error tiers, not only the exact one: `--limit 3` on a phrase returned fifty sessions. (#3348)
- The per-prompt hook reads the dedupe file once per prompt instead of four times. (#3380)
- VS Code Copilot Chat sessions are found by asking VS Code for its session directories instead of walking the whole `User` folder. (#3169)
- The pre-tool hook says the outcome or nothing: a count and a date is the pointer the code beside it argues against, spent on the one line the hook gets. (#3431)
- A short question has to have matched what it is about, not the working word beside it — the ranking now carries which of the question's naming words each session matched. Cross-paired false fires 7 of 51 to 5, with no other bench arm moving. (#3431)
- A match that covers a session outranks one that brushes a marathon. (#3431)
- "Most re-used" counts what an agent asked for. A per-prompt push is deja serving a session, not evidence it mattered, and it was 60–70% of that number. (#3431)
- The install report names every file a target wrote: nineteen targets wrote the CLI skill and none said so, and kimi, omp, cline, dsh, goose, openclaw and grok each wrote a second file it never mentioned. (#3431)
- The credit line hands the agent the sentence to write instead of asking it to decide whether to write one. (#3431)
- The reading and indexing phases move per file and count messages as they land, and the sidecar builders are a phase rather than eight silent seconds. (#3374, #3376, #3416)
- Every injection records the session it went to, the point-of-action hooks and the MCP tools included: 492 tool-time injections and 577 answers an agent asked for carried no receiver, so nothing deja said could be paired with what the agent did next. (#3440, #3442)

### Fixed
- A compaction summary does not name a session: the preamble introducing it is stripped before anything reads the turn, so the summary read as the first thing a person said and 23 of the last 800 sessions on a real store were called "Summary: 1. Primary Request and Intent…". (#3439)
- The per-prompt hook stands down on a background job finishing: the notification block was dropped and the host's own paragraph above it was answered as a question. (#3442)
- A note rewritten to the same length is seen: size and modification time cannot tell an accepted decision from a rejected one on a coarse clock, so a young stamp is confirmed by content. (#3439)
- Claude Code: a record Claude Code wrote itself — `isMeta`, the hook's own line, a compaction summary — is not the person talking. (#3268, #3334, #3163, #3178)
- Copilot CLI: a skill the host injects is the host's, not the person's words, and a command that exited non-zero says so. (#3306, #3371)
- Roo Code and Cline: the `environment_details` block is not the person's words, and a task's tool calls, results and command output are indexed as its commands, files and tool output. (#3256, #3270, #3296, #3331)
- Zed: the editor's summary of a mentioned thread is not what the person said, what a tool returned is indexed, a thread's own order is its clock so two identical turns both survive, and the block takes the file's line ending. (#3292, #3332, #3335, #3338, #3232)
- Antigravity: approving a plan is the IDE's record, and an edit through `replace_file_content` leaves a files record and an edit record. (#3280, #3327)
- opencode: a part flagged synthetic is the harness's, a subagent run knows the session that spawned it, a session is named by opencode's own title unless that says less than its first line, and a switched-off MCP entry does not count as wired. (#3194, #3300, #3302, #3316)
- Gemini and Qwen: `toolCalls`, the `functionResponse` turn and a `tool_result` record are the work, indexed like the others — error included. (#3282, #3294)
- Grok: a `tool_call`'s `rawInput` is the command and the file it worked on, and the sources row counts the database beside the JSONL. (#3227, #3240, #3286)
- aider: the banner, a multi-line output block and a logged slash command are not the assistant or the person speaking, and a flow list under `read:` is a list. (#3207, #3229, #3249, #3312)
- Kimi Code: only what the person wrote is a user turn. (#3201, #3235)
- Cursor: a turn is dated by the stamp Cursor wrote into it, and the hook's `conversation_id` is the session — so recall is not repeated on every prompt. (#3288, #3352)
- Continue: a placeholder title, or the first line again, is no title. (#3275)
- Hermes: `HERMES_HOME` moves install, parse and doctor with it; a session belongs to the directory it was recorded in and is titled by the question rather than the greeting; the memory provider loads on the build it finds. (#3206, #3242, #3250, #3252, #3258, #3273, #3341, #3391)
- The per-prompt hook stands down on the host's own turns, answers the same question once an hour rather than once per tick, and reads the question before the paste above it — including under an `attached_files` block. (#3164, #3184, #3337, #3350)
- A compaction forgets every key the session filed memory under, so what it threw away can come back. (#3308, #3354)
- `deja doctor`: the rows say when a transcript went unread for cline, OpenClaw and Antigravity; the gemini row counts its own chats rather than another harness's store; Codex caches, Kimi's `state.json`, Continue's session list and Copilot's `vscode.metadata.json` are files deja reads, not files it missed; a store with one locked root is reported as partly readable; and the advice names the target that adds the hook. (#3298, #3304, #3310, #3314, #3318, #3320, #3322, #3363, #3378, #3398, #3400, #3408)
- Install and uninstall: a build that is not the installed deja leaves the wiring alone, Claude Code's writer collapses its own repeated entries, `--all` says where the hooks are, a run that changes nothing still names the files it wrote, no snapshot is taken of a file the same run created, a snapshot holding only deja's own block is deja's to remove, a JSONC config with trailing commas is JSONC, and the vscode and roo installs name every host they write. (#3230, #3234, #3237, #3246, #3342, #3394, #3423, #3424, #3425)
- Install and uninstall, per harness: grok names `config.toml` and `user-settings.json` with the hooks; gemini-auto names the extension and the switch it leaves on; goose stops deleting the reader's own `.goosehints` and names the hooks plugin it removes; openclaw gives its hook switch back; the DeepSeek uninstall leaves a layer dsh can load; prime keeps the comments in its settings; qwen reports the hooks it rewired. (#3186, #3198, #3205, #3209, #3221, #3224, #3226, #3238, #3244, #3253, #3272, #3389, #3127)
- Uninstall keeps a directory the reader already had: the prune could only test "nothing else is in it". (#3430)
- The index does not record a store it could not read as read, so a locked sqlite store is parsed again on the next pass instead of being called up to date. (#3177, #3191, #3430)
- A search no longer waits on the tail of a session being written: past eight megabytes of new bytes the append is treated as rewrite-grade and the answer comes from the snapshot. (#3430)
- `deja forget` takes the cached session-start digests with the sessions. (#3413)
- `deja fix`: the ignore rule reaches the sightings, an unconfirmed remedy from an excluded tree is withheld, and `--json` and the MCP mode name the file that changed next. (#3260, #3262, #3404, #3418)
- `deja friction`: the go module, sqlite and linker walls read as errors, two failures that share a URL no longer print as one row twice, and a missing table is the server's line. (#3375, #3393, #3402, #3419)
- `deja files` answers a quoted topic the same as the same words unquoted. (#3410)
- The refusals say what to do: `--limit` names what `last` and `blame` take instead, and a question that starts with a dash is told about the separator. (#3396, #3406)
- The install report and the site count twenty-five harnesses, and the MCP harness filter names the registry rather than nine harnesses from 2026. (#3347, #3364, #3383, #3387)
- Windows: a workspace path folds into the project key the way a POSIX one does, and two fixtures that pasted a filesystem path into JSON stopped failing the leg they were written for. (#3218, #3427)

## [0.19.4] - 2026-09-07

The release where every agent on the machine gets the same memory. Amp,
prime-agent and VS Code Copilot Chat are wired from nothing — the last of them
joining as the twenty-third harness — and the ones that were already wired now
reach the moments recall is worth most: the first prompt, the failing command,
the file about to be edited, the turn after a compaction. The agent now says
"déjà vu" with a date and a session id when it reuses what it was handed.

### Added
- VS Code Copilot Chat: its chat sessions are indexed (`chatSessions/*.jsonl`, the append log VS Code writes since 1.109), the twenty-third harness — contributed by @Sora-bluesky. (#3088)
- VS Code: `deja install vscode` writes the `mcp.json` Copilot Chat reads in agent mode, one per host present (Code, Insiders, VSCodium), and a `deja.instructions.md` applied to every chat so recall arrives without being asked for; measured on VS Code 1.134.0 from the MCP traffic. (#3102, #3107)
- Amp: `deja install amp` writes the MCP server the way `amp mcp add` does; `amp-auto` adds a plugin under `~/.config/amp/plugins` that hands the project digest to the first turn, per-prompt recall after it, and the repair beside a failed command in the same turn. (#3118)
- prime-agent (PrimeIntellect): `deja install prime` writes the MCP server into `~/.prime/agent/settings.json` and an extension that opens a session with the project digest, recalls on each prompt, forgets on compaction, and adds `/deja`. Its `tool_call` and `tool_result` events never fire on 0.9.1, so the fix pair is deliberately not wired there. (#3125)
- Kimi Code: the session digest rides the first prompt and a compaction forgets what it threw away, measured on 0.28.1 — `UserPromptSubmit` is the one event whose output reaches the model. (#3121)
- Roo Code: `deja install roo` names deja's own tool in the entry's `alwaysAllow`, so Roo stops asking before every recall — without it a non-interactive run waits at the first one forever. `deja resume` prints `roo --session-id <uuid>` for tasks the Roo CLI created, with the workspace it must run in; editor tasks still reopen from the editor. Measured against a recording endpoint on @roo-code/cli 0.1.17 with the 3.53 extension. (#3130)
- `deja files --json`: the ranked rows a caller had to parse out of padded columns, with the three counts the ranking is built from. (#3106, #3124)
- pi and omp: `@vshulcz/pi-deja`, the recall extension as a pi package — `pi install npm:@vshulcz/pi-deja` — session-start digest, per-prompt recall and `/deja`. (#3095)
- pi and omp: a failed command gets its repair in the same turn (`tool_result` is the event whose return reaches the model; omp's bash tool reports the exit code in `details.exitCode`), a compaction forgets, and a file's history arrives when the agent reads it — the step before the edit, since neither harness has a pre-edit hook. (#3112, #3113)
- Cline: the repair for a failed command arrives in the same turn, appended to the tool result by the message builder; `hook-tool-after` learns cline's `run_commands` and grows `--plain`. (#3098)
- OpenClaw: `@vshulcz/openclaw-deja`, the recall plugin as a package — `openclaw plugins install clawhub:@vshulcz/openclaw-deja` — and a local session opens with the project digest via `before_agent_start` (74 ms and 1.6 KB on the first turn, nothing after). A compaction now forgets what it threw away, and the digest moved to the phase hook that fires once a turn. (#3058, #3087, #3123)
- DeepSeek Harness: a session opens knowing what the project settled — a second `systemPrompt.context` contributor ahead of per-prompt recall, once per session. (#3094)
- Antigravity: `PreInvocation` answers the question instead of only opening with the digest (the invocation counter is zero-based and restarts every turn); the repair arrives at the failing command; what a compaction threw away is served again; and the project is recalled when the payload names no workspace, read from `cache/last_conversations.json`. Measured on antigravity-cli 1.1.13. (#3059, #3063, #3077, #3078)
- Install: the proof opens with the questions this machine asked more than once, and one of them — `26 questions asked more than once on this machine — one of them: "…"`. (#3071)
- The first session after an index build hears what was indexed — `deja indexed 1,444 sessions from 19 agents on this machine — 27 questions asked more than once` — once per index. (#3074)
- Once a week, session start says what the week looked like: `deja this week: 41 recalls, 3 déjà vu — deja stats --card`. (#3070)

### Changed
- The line an agent says when it reused a recall is now `déjà vu: <what you asked then> (<agent>, <date>, deja:<id>) — reusing it.`, on every surface that asks for one; a question this machine already asked is quoted back in its own words. The line names the session the digest shows, not the top of the ranking. `deja stats` counts the new shape alongside "deja-vu recalled". (#3108)
- `deja stats --card` leads with the questions asked more than once, the week moves to the supporting row, and the footer links to the repository. (#3069)
- The README, the site and the npm page lead with what the harness count is for: one memory every agent on the machine reads. (#3109)

### Fixed
- Qwen Code: the repair sat on `PostToolUse`, which qwen fires only when the tool succeeded — the one event that never comes at a failure. It moves to `PostToolUseFailure`, whose return is appended to the tool result the model reads, and the old entry is dropped on install. The failing output arrives under `error` inside qwen's own `Command: / Output: / Exit Code:` block, which is unwrapped before the signature is taken. Measured on qwen-code 0.20.0. (#3126)
- Qwen Code: `install qwen-auto` reported "unchanged" while it rewired the hooks, because it returned only the MCP half of the same file. (#3127)
- Grok Build: `SessionStart` and `PreCompact` matchers that grok never fired are dropped and corrected on machines that already installed deja; hook inputs read grok's camelCase; agents it spawns are reached. (#3085)
- Codex: the plugin manifest carries its icon, spells the URL keys the way the spec does, and names its hooks file as one path. (#3132)
- DeepSeek Harness: the plugin declares the DSH release it was measured on. (#3129)
- Zed: the `/deja` fix from #3020 never shipped because `extension.toml` still said 0.1.0; the version moves and CI fails when a change outruns it. (#3081)
- pi: the generated `/deja` command declared `args` twice, so pi loaded the extension with no deja at all. (#3096)
- A compiler error is one wall wherever the line moves: the position is masked on the repair signature, so adding an import no longer turns one recurring error into a new one on every edit. (#3080)
- Search: offer `--limit` in bash, zsh, fish, and PowerShell completions. (#1929)
- The README GIF recipe is complete and reproducible. (#1832)
- Release scripts run npm, tar and unzip without a shell, and the digest marker constants are named as markers — the plugin scanner the Codex plugin catalogue pins reports nothing on main. (#3114, #3116)

## [0.19.3] - 2026-09-04

A release about the moment memory is worth most: the turn a command fails, the
turn after a compaction, the first prompt of a session. Four harnesses were
wired for less than they can take, and the measurements that found it are in
each line.

### Added
- Codex takes recall on the prompt itself, not only at session start.
  `UserPromptSubmit` carries the same payload Claude sends, so `hook-prompt`
  reads it unchanged; measured on codex 0.149.0, the answer reached the model in
  every request with the event wired and in none without it. (#3041)
- Codex clears what a compaction threw away, so recall can serve it again
  instead of treating it as already shown. (#3044)
- Gemini gets the fix pair where a command fails: what an `AfterTool` hook
  returns is appended to the tool result, which is exactly where the repair
  belongs. Its shell tool is `run_shell_command` and its output arrives as
  `llmContent` inside a frame whose `Output:` marker stopped a build failure
  reading as an error — all three are handled. `BeforeTool` is deliberately not
  wired: it fires, and what it returns never reaches the model. (#3049)
- Qwen takes the session digest again and the fix pair for the first time. On
  qwen-code 0.20.0 `SessionStart` is consumed, where an older build ignored it,
  and `PostToolUse` is too: measured against a stub endpoint, 0 of 2 requests
  carried a deja block before and 2 of 2 after. (#3050)
- Cursor gets recall at the moment of the action. It maps Claude's event names
  onto its own and reads `~/.claude/settings.json`, so it has been running
  deja's pre-tool, post-tool and pre-compact hooks for anyone who also has
  Claude Code and none of them for anyone who does not; its own `hooks.json`
  now carries the same three. (#3052)
- A slash command for harnesses deja ships no command file into. Gemini and
  qwen both ask an MCP server for `prompts/list` on startup and put what comes
  back behind a slash; deja answered `-32601`. It now offers one command that
  runs the recall and hands back the result. (#3051)
- opencode recalls the repair when a command fails, carries recall into a
  spawned agent, and its npm package ships the same channels the installed
  plugin has. (#3033, #3027, #3036)
- `deja sync export --include-imported` hands over what arrived from other
  machines, under the names it had where the work happened, so a machine in the
  middle of a chain is no longer a dead end and a migration off it carries
  everything. Off by default; an export that holds work back now says so
  instead of reporting nothing changed. (#2962, #3053)
- `deja install hermes-auto` writes a Hermes memory provider (`deja-memory`):
  `hermes memory setup` lists it beside mem0 and supermemory, and with
  `memory.provider: deja-memory` the model gets deja's recall before each turn
  plus `deja_recall`, `deja_fix` and `deja_blame` as tools. (#3035)
- Amp (Sourcegraph) threads are indexed, from `~/.local/share/amp/threads` or
  `$XDG_DATA_HOME`; `DEJA_AMP_ROOT` overrides. (#1714, #2986)
- A transcript the client deleted stays in the index. Claude Code removes
  transcripts older than `cleanupPeriodDays` (30 by default), and the next
  incremental pass used to drop the session with the file. A file that is gone
  while its store is still there is now kept and stays searchable; `deja
  forget` remains the way to drop one on purpose. A store that vanishes whole
  is dropped as before, and a full `deja index --rebuild` still reads only what
  is on disk. (#2970)
- The proof after `deja install --auto` opens with the error this machine keeps
  hitting — "this machine has hit `command not found: timeout` in 18 sessions"
  — above the recent-sessions list. The same rule and threshold as `friction`;
  nothing is claimed when nothing recurs. (#2966)
- `DEJA_TRACE=1` names where a slow search spent its time — the lock, the store
  walk, the ingest — on the path every CLI search takes, not only in the
  session-start hook. (#3021, #3054)
- On a large store, a session is ranked by the turn that holds the fact rather
  than by the whole transcript. (#3017)
- `deja stats` gives the card a way onto a social post. (#2959)
- The DeepSeek Harness plugin carries a root manifest, so the catalogs see it
  inside the monorepo. (#2981)
- The Claude plugin carries a `plugin.json` at the root the layout directories
  read, and the keywords the directory search needs. (#3011, #3005)

### Changed
- The index format moved: this release rebuilds the index once on first run.
  Arabic, Hebrew, Thai and the Indic scripts are tokenized as words rather than
  as single letters, Thai gets the bigrams the other unspaced scripts have,
  grammar-only CJK bigrams are no longer stored, and a posting's session id is
  written as a delta — measured at 5.9% to 16.1% smaller `buckets/` across four
  stores.

  **A read-only index directory needs its index replaced by hand.** deja serves
  a directory it cannot lock without a version check, by design, so it cannot
  rebuild one in place: an index written by an earlier deja inside a read-only
  image will make `deja search` exit with `index written by another version of
  deja` until the directory is rebuilt somewhere writable and copied in. The
  alternative was reading old posting bytes under the new rule, which returns
  session ids that are wrong without saying so.

### Fixed
- A session start stops repeating the last one. Without changed files to match
  against, the candidate pool was three deep and a digest serves three, so
  every candidate had just been served and the novelty ordering had nothing to
  promote — and the builder sorted by recency again on the way past. Measured on
  a seeded store of 300 sessions, five consecutive starts named 3 distinct
  sessions before and 12 after, at 1 ms of a 22 ms session start. (#3048)
- Codex fires `SessionStart` again with source `compact` after every
  compaction, which the matcher `startup|resume` never matched — one `codex
  exec` run compacted 12 times and the agent came out of each with nothing.
  2 of 13 requests carried a recall block before, 13 of 13 after. Install now
  rewrites the matcher on an entry it owns, so the fix reaches machines that
  already have one. (#3046)
- Cursor's payload is read as cursor sends it: the shell tool is called `Shell`,
  the output arrives under `tool_output` as a JSON document inside a JSON
  string, and `cwd` is empty with the project named in `workspace_roots`. deja
  read none of the three, so the fix pair had no error to match and recall
  answered for whatever directory the hook was spawned in. (#3052)
- opencode asks from the project rather than from wherever opencode was
  started, one failed call no longer costs the session its memory, and a query
  naming one of deja's own flags reaches the store. (#3028, #3024, #3014)
- Every `/deja` runs a search instead of deja's first word, across the harnesses
  that ship the command. (#3020, #2988, #2958)
- goose honours the recall kill switch, refreshes recall on every prompt rather
  than only under the wrapper, puts the session-start recall where goose reads
  it, and indexes the command it ran with what the command printed. (#2980,
  #2963, #2954, #2951)
- OpenClaw 2026.8 keeps sessions in
  `agents/<agent>/agent/openclaw-agent.sqlite`; deja read only the JSONL files
  it left behind, so nothing written since the upgrade reached the index. The
  store is read now, including the sessions an earlier reset window kept.
  (#2994)
- `just`, `task` and `mise` are recorded, so the pre-tool hook can speak about
  the commands a repo actually runs. (#2999)
- The environment block stops blaming a missing tool for every wall it names.
  (#3012)
- A duplicate DeepSeek Harness registration stands down instead of failing the
  whole profile. (#2990)
- `deja install` ends the flags before the query for cline, pi and hermes too.
  (#3015)
- doctor's subagent row names the variable as the remedy rather than as the
  cause. (#3008)
- The release no longer takes plugin tags into goreleaser's version templates.
  (#3013)

## [0.19.2] - 2026-09-01

A release about what deja hands over and whether it can be trusted. The week's
bug hunt landed with it, and the theme that runs through both is the same: a
surface that says something it does not know is worse than one that says
nothing.

### Added
- prime-agent sessions are indexed. (#2623)
- goose sessions reach recall whichever way a person got to goose — over ACP
  from an editor, from the CLI, from a chat gateway, or from a run that kept no
  session. The filter was an allow-list of `session_type = 'user'`, so a store
  full of ACP sessions had none of them in recall and nothing said why.
  (#2874, #2908)
- `--json` for `fix`, `friction` and `how`, in the shape `docs/json-output.md`
  describes. Each envelope carries the counts a caller cannot recover from the
  rows — how many recurring errors there were before `--limit`, the threshold a
  row had to clear, what a rule withheld — so an empty result can be told apart
  from a hidden one. (#2895, #2919)
- `deja bench block` scores what the block says rather than what it chose. The
  other three benches print the same numbers with the block emptied; this one
  goes to zero, and its baseline — the newest turns of the top hit — scores zero
  so an arm above it had to find the answer. (#2940)
- `recall_context` says how many other sessions matched the question, so a
  digest chosen from forty no longer reads as the only thing deja held. (#2894)
- `doctor` names the sessions stamped ahead of the clock. (#2824)
- `DEJA_EMBED_OFF=1` switches off the localhost probe for an embedding endpoint.
  Without it a machine running Ollama answered differently from one that was
  not, including in deja's own test suite. (#2937)

### Changed
- Every answer that carries text out of a transcript says that the text is
  untrusted — the MCP tools, the resources, the hooks, and the commands `how`
  and `fix` hand an agent. Pinned by a property test per surface rather than
  case by case. (#2844, #2847, #2848, #2850, #2852, #2859)
- A growing session is read from where the last pass stopped rather than whole,
  for goose, qwen, omp, OpenClaw and prime; the grok, hermes and zed stores are
  asked only for what changed. One appended turn against a 300-file store went
  from 1045 ms to 58 ms. (#2812, #2813, #2818, #2876, #2877, #2902)
- `install` reads a config before writing it: it updates the entry that already
  runs deja instead of adding a second, keeps the shape and the comments it
  found, edits an inline block that opens and closes on one line, asks about
  every host a target touches before writing any of them, and refuses a config
  it cannot read rather than writing over it. (#2648, #2737, #2739, #2743,
  #2747, #2748, #2752, #2753, #2755, #2756, #2758, #2761, #2778, #2780)
- A rephrased question reaches its answer through the project's own words, and
  ranking pays for coverage only on the words that identify something.
  (#2480, #2484)

### Fixed
- The block quotes the sentence that concludes rather than the opening of the
  message that carries it, and a message kept for its code fence no longer
  takes the slot the conclusion needs. Measured against a real store, blocks
  carrying what the session settled went from 265 of 456 to 440 of 452 at the
  session-start budget. (#2906, #2913, #2922)
- A sentence with no stopword in it is no longer read as a listing dump, and a
  sentence that opens with a number is no longer read as a numbered one. The
  first rule counted path-shaped fields and never read the count; over 277375
  prose lines of a real store it dropped 20294 of them, of which 55 were
  listings. (#2916, #2918)
- `blame` and the search excerpts stopped quoting `wc -l` output and runs of
  paths as the passage that explains a file. The snippet path had its own
  smaller set of filters and no notion of a listing at all. (#2925)
- `friction` sanitises a recorded error line before printing it. An ANSI escape
  recoloured the rest of the terminal and U+202E reversed the reading order of
  everything after it; every other surface already ran a recorded line through
  the same sanitiser. (#2910)
- An empty store is described as an empty store. `recall`, `blame`, `how` and
  `fix` blamed the query instead, and a store emptied by `forget` was reported
  as a first run. (#2862, #2863, #2865, #2882)
- A remedy that names a scratch file is not mined as one. On a real store 38 of
  186 confirmed pairs pointed at a path under a temp directory — a command
  nobody can run by the time deja serves it. (#2903)
- pi transcripts keep their session header when a parse resumes past it, so an
  appended turn lands in the session it continues rather than beside it. (#2901)
- A promoted note stays ahead of the transcript it distils, on the error and
  relevance tiers, in the per-prompt hook, and when either side arrived by sync.
  (#2804, #2821, #2823, #2838)
- A recalled session that names what was asked is no longer disowned by the line
  above it, and the recall header stopped claiming a query it did not match.
  (#2789, #2831, #2834, #2837)
- `how` counts the sessions the policy hid that match the question, not every
  withheld session on the machine. (#2765, #2792, #2795)
- CI status is not promoted as what a session concluded, a bullet of a document
  the agent wrote is not recalled as a decision, and harness envelopes stay out
  of the session-start block. (#2736, #2741, #2745, #2774)
- The trust policy is read from a home directory or from nowhere, and with no
  home directory deja writes nothing where it happens to be run. (#2782, #2788)
- `handoff` marks where the quoted session starts and ends, and sanitises the
  digest `--exec` hands to the next agent. (#2867, #2891)
- The week's bug hunt, 124 fixes, merged. (#2616, #2733)

## [0.19.1] - 2026-08-28

A patch release about reach and honesty. Memory now arrives where an agent
actually works — inside the subagents that do the reviewing and the hunting —
and several surfaces stopped claiming more than they knew.

### Added
- A spawned agent gets the memory its parent has. Subagents receive no session
  start and send no user prompt, so nothing deja injected ever reached them:
  289 of 328 sessions on the store this was measured against were subagents,
  and 1% of them saw a recall. `PreToolUse` rewrites the instructions the
  parent wrote, which is the one place a subagent can still be reached. The
  same for opencode, through its plugin's `tool.execute.before`. (#2143, #2147)
- The block says so when the question itself was asked before, rather than
  saying a session matched the subject. Matched on the question's terms rather
  than its wording: the exact-wording counter saw a fifth of the repeats this
  store holds, and 6.5% of substantial questions are asked again in a later
  session. (#2276)
- A session start leads with what the project has not been told yet. (#2047)
- A failing test is read as friction by its name. `--- FAIL: TestName` is the
  most common failure in a Go repository — 2,318 of 6,744 error lines here —
  and it was rejected with the summary lines that identify nothing. (#2180)
- The policy file names the directories deja skips, so a scratch tree can be
  kept out of recall by rule rather than by hand. (#2060)

### Changed
- `recall`'s description says what works. It asked for a single specific token
  and warned that several words are ANDed; measured over 760 real calls,
  nothing matched zero times at any query length and the longer questions came
  back with more. (#2342)
- `doctor` stops reporting deliberate skips as files it could not read. The row
  said "1192 not recognised here" where 596 were subagent transcripts deja is
  written to leave alone and 452 sat in a `.tmp` directory. (#2345)
- The relevance tokenisation moved to the package both `index` and `prompt`
  build on, which also took a cross-package dependency out. (#2136)

### Fixed
- A directory the trust policy says is not to be recalled is now out of reach
  everywhere. The rule was applied at one call site — the CLI's own search —
  so `doctor` printed "not recalled" while the per-prompt hook injected those
  sessions into every message. (#2323)
- One rare word no longer decides which session answers. The gate that checks a
  session says the subject was given a single term, the rarest by corpus IDF,
  and a small corpus crowns ordinary nouns: the session that settled a question
  was dropped for saying "orders worker" where the question said "service". The
  block's two slots also stopped holding two copies of one answer. (#2328)
- A cross-script CJK hit is scored on the text that matched it. It was counted
  through the fold and then scored against the surface text, where the query's
  words appear nowhere, so a record that matched twice ranked below one that
  matched once. (#2157)
- Japanese grammar bigrams stop triggering auto-recall. The fallback dropped
  "pure grammar" through a Chinese closed class, while Japanese grammar lives in
  hiragana — and the bigram slide crosses word boundaries, so ての in すべて|の
  is not even a word. Contributed by
  [@Sora-bluesky](https://github.com/Sora-bluesky). (#2262)
- The fix pair is held to what a remedy is: the error must stay gone for the
  rest of the session, the command must not have failed itself, and reading
  something is not fixing it — a `grep` for the symbol the compiler named
  satisfies "the command names what the error named" by construction. And
  `deja fix`'s own output no longer counts as a fresh error, which had made
  asking about an error record that it happened again. (#2166, #2169, #2172,
  #2174)
- An agent's own scratch sessions and the transcript's record of a deja call
  stay out of recall, so the tool stops answering with itself. (#2054, #2068)
- The injection cooldown carries across agent sessions in a project, so a
  fleet of agents opened together does not re-serve one session to each.
  (#2041)
- A whole transcript is no longer re-read to verify an append. The rewind guard
  hashed every byte before the append point on each search that saw the file
  change, which for the session being written is every search: 1.70s per call
  against a 250 MB transcript, now 0.36s. (#2154)
- `deja last`, `show`, `forget` and `remember` complete their own flags.
  Contributed by [@vaibhav8a](https://github.com/vaibhav8a). (#2148, #2021,
  #2027, #2028)
- Six days of the bug hunt, 148 changes, went in behind these: the session
  start and the file line stay inside the checkout's own project rather than
  one whose name ends the same way, `forget` takes what it forgot out of the
  injection log, the usage log records which projects a digest was built from,
  and `doctor`, `view` and `friction` name the rule when a trust policy empties
  what they show. (#2351)


## [0.19.0] - 2026-08-26

Two things, mostly. deja now installs the way each harness installs everything
else — as a package in its own format, from its own catalogue — and a long bug
hunt went through the parts that report: the usage log, sync between machines,
and what `doctor` says when a file will not open.

### Added
- Gemini CLI installs this repository as an extension: `gemini extensions install https://github.com/vshulcz/deja-vu` brings the MCP server, the context file and the `deja-search` skill. The manifest sits at the root, which is also what the extensions gallery crawls. (#1725)
- The Agent Plugins manifests are at the root too, so any host that reads the portable v1 format — Qwen Code among them — installs deja without a format of its own. (#1769)
- A Kimi Code plugin, in `extensions/kimi`. One `/plugins install` gives Kimi the MCP tools, the shared skill, a `/deja:recall` command and recall on every prompt through its `UserPromptSubmit` hook. It stands down where `deja install kimi` already wired the same thing, so having both is not two of anything. (#1573)
- A Grok Build plugin, with a licence in every bundle. (#1591)
- A dsh plugin, and auto-recall that reaches the model rather than the transcript. (#1552)
- The Codex plugin carries its own hooks: a marketplace install now brings session-start recall, per-prompt recall and pre-compaction capture with it, instead of only tools and the skill. It stands down where `deja install codex-auto` already wrote the same hook into Codex's own hooks.json. (#1583, #1584)
- The opencode, dsh and Zed packages live in this repository, so a change to deja and the change to its packages land together. (#1562)
- `deja embed` reaches an authenticated endpoint: `DEJA_EMBED_KEY` for any OpenAI-compatible URL, and `OPENAI_API_KEY` only for an HTTPS `api.openai.com` one — never forwarded implicitly to localhost or a third party. Contributed by [@chrisgeo](https://github.com/chrisgeo). (#1919)
- PowerShell completion. (#1803)
- The fix pair arrives when a command fails, rather than waiting to be asked. (#1550)
- Recall says when a hit is the other machine's copy of a session this one already holds, and when a session's date is later than this machine's clock. (#1754, #1776)
- An index run says how many lines it could not read, and `doctor --json` carries the sync state that used to exist only on the screen. (#1839, #1994)

### Changed
- The release archive carries `gemini-extension.json` and `GEMINI.md`. `gemini extensions install` reads the manifest out of the release asset rather than from git, so the documented command failed on every published version until now. (#1935)
- `deja install` refuses rather than guesses in the places it used to guess: a guidance block it cannot bound, a `cordis.patch.yml` missing one of deja's markers, a goose `extensions:` list where a mapping was expected, an opencode.jsonc that uses trailing commas, a relative `XDG_CONFIG_HOME`, and a machine with no home directory. It also writes a config back with the line endings it already had. (#1666, #1669, #1691, #1694, #1696, #1698, #1700, #1702, #1706)
- The usage log holds one definition of an event, one week for every counter that shows one, and a record size it can actually hold. Rotation keeps the newest events instead of emptying the file. (#1918, #1921, #1923, #1974, #1981, #1986)
- `doctor` explains rather than accuses: a corrupt sidecar is not a missing one, an unreadable peers file is not "no machines", a transcript it may not open is named instead of the harness format being blamed, and a peer stamp ahead of this machine's clock is said out loud. (#1711, #1713, #1749, #1841, #1856, #1860, #1961)

### Fixed
- Two syncs at once no longer lose machines, and one machine named in two letter cases is one machine — in the watermark, in `doctor`'s count, and in how a host name is matched, which is the way ssh matches it. (#1854, #1868, #1877, #1879, #1884)
- ssh gets a connect timeout, so a sleeping machine costs seconds rather than minutes. (#1773)
- A credential in an uppercase `_KEY` environment assignment is stripped at index time, and so is a secret whose JSON arrived with its quotes escaped. (#1766, #1958)
- The PATH offer in `install.sh` sat behind a condition `curl | sh` can never meet, so nobody installing the documented way was ever asked. (#1852)
- A truncated line — in the log, in the snapshots file, in the hook's dedup file — is closed before the next event is appended, rather than merged into it. (#1910, #1947, #1966)
- Unicode folding covers Arabic hamza and madda, the three Greek marks written on a spacing diaeresis, and letters carrying two marks. (#1836, #1873, #1895, #1914)
- The statusline gives the filename up before the memory half when the bar is too narrow, and uses the count form when even the title will not fit. (#1881, #1904)
- The index resolves a shared session id the same way every time, counts the empty transcripts of the last build rather than of the process, follows a store directory that is a symlink, and says a store went away instead of greeting the reader as new. (#1745, #1764, #1851, #1998)


## [0.18.0] - 2026-08-22

Two new harnesses, the resume column finished, and a recall path rebuilt around
one question: does the block deja injects carry the answer, or only the word the
question happened to share.

### Added
- omp (Oh My Pi) is the nineteenth harness deja indexes, contributed by [@hanchang](https://github.com/hanchang). Sessions are pi-format JSONL under `~/.omp/agent/sessions`, with profiles and XDG relocations read too; it gets the shared skill, a `/deja` command and auto-recall through an extension on its context event. (#1455, #1520, #1523, #1525)
- DeepSeek Harness is the twentieth. Sessions are zstd-framed JSONL under `$DSH_HOME`; MCP, the shared skill and a `/deja` command arrive through the home patch layer, and `deja install deepseek-auto` adds a plugin on `agent/pre-step` that puts recall in front of the model. Verified against a running dsh with no tools in play. (#1518, #1519, #1524, #1527)
- `deja resume` now reopens qwen, Gemini CLI, Cursor CLI and OpenClaw sessions. Each takes the id deja already indexes — OpenClaw through the session key its store maps that id to — and the command carries the project directory where the harness scopes its session list to one. (#1528, #1529)
- Grok's spawn tree: `session_kind`, `parent_session_id` and `agent_name` come out of `summary.json` into the session record, `--json` carries them, and `show` names the session a child was forked from and the children a parent spawned. deja never infers an edge the harness did not write. (#1385)
- One `deja sync` command for every machine, a timer that keeps them in step without being asked, and a report that shows a sync going stale — plus the machine a memory came from, carried across. (#1430, #1432, #1433)
- The session format registry is published as pages, so what deja reads out of each store is a thing you can link to. (#1434)
- Zed reads the shared skill, and that skill is its command. (#1426, #1428, #1429, #1526)

### Changed
- A growing Grok session appends to the index instead of rewriting it. The stream is parsed from its last safe offset when the prefix hash still matches; a rewind that truncates and regrows still reparses in full. On a 1.7 GB store a live session made every search pay for a full rewrite. (#1522)
- CLI search stops waiting for that rewrite. Appends stay inline; rewrite-grade work goes to the detached warmup and the search answers from the index it has, the way the MCP tools already did. `--rebuild` still waits. (#1521)
- A Claude subagent's transcript is its own session, keyed by `agentId` with the parent recorded, rather than a copy of the parent folded under the same id. The registry no longer calls those files duplicates: the parent keeps the launch and a summary, the turns and the tool stream live only in the child. Still behind `DEJA_INCLUDE_SUBAGENTS=1`, now for index size rather than for work that was said to be there twice. (#1384)
- Recall quotes the line that settled something instead of the densest mention of a word, carries two lines past the match, and leads with the session that concluded rather than the one that echoed. The per-prompt budget went to 1536 bytes and a single-rare-word match gets half of it. (#1463, #1467, #1475, #1488, #1492, #1493, #1504, #1506, #1507, #1511, #1514)
- Ranking fuses its two views with reciprocal rank fusion rather than one overriding the other. (#1491)
- How rare a word is takes the rarer of two verdicts — one document per session, and one per message with each session capped. Sessions alone called a subject word common because the marathons that hold most of a store all mention it; messages alone called the topic of one long session filler. On a replay of 59 real questions the block answered 43 against 41, with none lost. (#1450, #1536)
- The hook and the benchmark apply one bar, in one place. The benchmark had been measuring a gate the product did not have — three times, in three different ways. (#1438, #1516, #1517)

### Fixed
- Russian questions reach the same recall English ones do: filler is filtered by the same rule, short subjects and three-letter acronyms survive term extraction, a hyphen inside a word no longer drops it, and decision markers read as conclusions. (#1440, #1459, #1460, #1462, #1465, #1468, #1470)
- deja stops quoting itself. A past session saying it had no memory, the harness checks run against the tool, and a scripted probe are not history worth recalling. (#1499, #1500, #1502, #1503)
- The block no longer opens with the message being typed, nor with the top of a transcript that merely mentioned the word. (#1439, #1458, #1479)
- Per-prompt recall carries the agent session id in opencode, pi and OpenClaw, so the same block is not repeated and a compacted context forgets what it was shown. (#1495, #1496, #1497, #1498)
- `deja install --auto` wired omp and DeepSeek Harness for MCP and left their auto-recall off, because the mapping from a detected harness to its deepest target was a hand-written switch and both landed after it. It comes from the target table now. (#1544)
- A plan is not a decision, and a stated outcome is. (#1478, #1501)
- The prompt benchmark can judge rarity at all: it ran on thirty chains of invented vocabulary where every word was rare, and now carries 300 sessions of ordinary working talk behind them. The false fires that surfaced were the fixture answering its own controls — chain filler quoted three of them word for word — and are back to zero. (#1448, #1534, #1535, #1538)


## [0.17.3] - 2026-08-19

Most of this release is one finding repeated in twenty places: deja measured
text in bytes and runes where the thing being measured was characters and
terminal columns. A Chinese session digested to nothing, a Japanese word was
indexed as two, a Russian conclusion was swallowed by its own preamble, and a
CJK title printed 110 columns on an 80-column terminal. None of it showed on an
English store, which is why it lasted.

### Added
- Zed's built-in agent is the eighteenth harness deja indexes. Its threads are one SQLite store under Zed's *data* directory — `~/Library/Application Support/Zed/threads/threads.db` on macOS, not the `~/.config/zed` that holds only settings — and every thread body inside it is a zstd frame, so reading them needs the `zstd` CLI beside `sqlite3`. With either tool missing, `deja sources` says which one rather than reporting an empty history. Both thread document generations parse, because Zed rewrites a thread in the current shape only when that thread is next saved, so a store mixes them indefinitely. (#183)
- A CLI skill for the harnesses that have no MCP: the same memory reaches them through the commands they can run. (#1371)
- `deja search --session` narrows a search to one conversation, for when the session is known and the line inside it is not. (#1362)
- The brief lays out for the terminal it is in rather than assuming eighty columns, which is what a split pane is not. (#1367)
- deja says what makes memory arrive at the two moments people notice it missing — the first run and the first recall. (#1277)

### Fixed
- Sessions written in Chinese, Japanese or Korean now carry their weight everywhere they were dropped: the digest returned nothing for them, recall skipped them as having nothing to say, a question asked in Chinese never counted as asked before, length normalisation did not apply, and repeat-question, superseded and related-note signals were all off. (#1341, #1343, #1345, #1347, #1349)
- Thresholds that read "forty characters" and counted bytes: the same-fact floor was forty characters for English and twenty for Russian, so a preamble swallowed the conclusion behind it; a wall's length was bounded in bytes, so a Russian line was held to sixty characters and a Chinese one to forty. (#1395, #1402)
- Text handed to an agent is cut on character boundaries. The answer line under a recall split the character sitting on its cap, because the walk back to a word boundary finds none in scripts that write no spaces. (#1391, #1405, #1410)
- Japanese words are indexed whole across the marks written inside them, rather than as the pieces either side. (#1392)
- Every one-line surface is measured in terminal columns rather than runes: the status line, the files table, the brief's recent lines and the search results each ran off the edge by half their width on CJK text. (#1386, #1398, #1399, #1401)
- The bytes a terminal acts on are stripped from every surface that prints recorded text — the files rows, the show header, the digest headers, and titles a harness authored. A file name can carry an escape or a carriage return on any Unix host. (#1400, #1406, #1407, #1408)
- Excerpts show the part of a message that answers the query rather than the first place a word appears, spend their whole window when the match sits at the end, and are chosen by where the query's terms meet. (#1319, #1323, #1326, #1328, #1330, #1332, #1387, #1393, #1396)
- A recall payload does not pay twice for one answer, and says how many sessions matched rather than only how many came back. (#1379, #1394)
- Incremental updates keep what they used to delete: the mined sidecars, new fix pairs, commands that became habits, and the evidence a pair needs until a second session confirms it. (#1296, #1297, #1299, #1382)
- Writes that replace a good file are atomic and carry their own temp name, so two deja processes cannot publish each other's half-written work. (#1302, #1389)
- Readers wait out an index swap instead of reporting the store as missing, and a hook running during a rebuild no longer decides there is no index. (#1375, #1378, #1390)
- A vector sidecar built for an earlier index is refused rather than trusted, and a rewritten records file no longer keeps the old generation. (#1356, #1358)
- The trust policy holds on every path data can leave by — embeddings, the exclude list, the whole brief screen, impact credits, and the MCP `how` tool, which was filtering by the CLI's rules rather than the agent's. (#1310, #1351, #1353, #1372, #1373, #1376, #1380)
- Windows paths are handled as Windows writes them: a project is spelled the same on every platform, project scoping matches both separators, a synced path is trimmed, and an import error is reported rather than surfacing as a raw syscall number. (#1287, #1288, #1289, #1315)
- Clocks that disagree no longer produce impossible answers: an event dated in the future stays out of today and this week, a session stamped ahead of the clock is not "-576000m old", and `ctx` dates sessions in the reader's zone. (#1314, #1404, #1409)
- An incremental update no longer writes credentials into the mined fix pairs: redaction applies on that path as it does on a full build. (#1300)

### Changed
- Co-occurring pairs are counted a shard at a time during indexing. (#1366)
- `deja sources` reports the files deja reads rather than the tree around them. (#1363)


## [0.17.2] - 2026-08-16

A question asked in plain words is answered better, and by how much is measured
rather than argued: hit@5 over a 1910-session corpus went from 28/40 to 31/40
with longmemeval unmoved. Three changes earned that; two more were tried, failed
to earn it, and are not here.

### Added
- The first-minute benchmark reports where an answer landed, not only whether it cleared a threshold. Once every answer is inside the window the counters that used to move stop moving, and a change lifting eight answers from rank 30 to rank 7 shows up in none of them. (#1260)
- The MCP tool list has a budget, because it is the one part of the surface every session pays for whether or not deja is used, and nothing was watching it. (#1260)

### Changed
- Satisfying the strict word-for-word match is worth a fixed number of places rather than the whole front of the answer. On a large history that match is usually one incidental session, and it led regardless of what the ranking made of it. (#1265)
- The ranking scores the whole question and its rare part separately and takes the better of the two, at a price. "How many bikes do I own" ranks on `many`, `bikes` and `own`, and the session that answers it holds only the rare one. (#1266)
- The excerpt under a result is the message that earned the rank, chosen with the ranking's own term weights rather than by counting words one each. It is the whole of what an agent reads before deciding whether a result is worth anything. (#1264, #1267)
- The mark is a cat that breathes, blinks through its eyelid, and now and then gathers itself and hops. (#1254, #1256, #1257, #1258, #1259)
- The site is repainted in the brand's colours, reads on a phone, and the home page has a rhythm and somewhere to land. (#1249, #1250, #1251)

### Fixed
- One decisive word is a match. A session holding the single rare word of a question was sorted behind every session holding two common ones before scoring had a say — rank 46 of 50 on the question that named the problem. (#1261)
- The relevance tier no longer serves what exact search hides. It never scanned records, so `--role=user` could come back holding assistant text, and an ordinary recall could be carried by a file list or a command. (#1263)
- Proximity is measured across the tightest window rather than between the first mention of each word, which is nearly the opposite of measuring it. (#1268)
- `deja doctor` had codex's hook backwards in both directions: a working hook read "untrusted", and one codex had never been shown — where it silently runs nothing, `codex exec` included — read "wired". (#1262)
- The stats card no longer prints a line out of the reader's own history onto an image made for sharing. (#1252)


## [0.17.1] - 2026-08-14

Four harnesses were being handed memory chosen before the user had typed
anything, and three sessions in four were helped without being told. Both were
found the same way: by driving every installed harness against a recording
endpoint and reading what actually arrived.

### Added
- Grok Build gets auto-recall: it reads hooks from `~/.grok/hooks` in the same shape Claude Code uses, and deja now writes all four events there instead of leaving grok with MCP alone.
- `/deja` reaches five more harnesses: opencode, Cursor and Roo Code read a markdown command, Gemini CLI a TOML one, and Goose a `slash_commands` entry in its config pointing at a recipe. It was Claude Code, Cline, Hermes and pi before.
- Recall against the question you asked, in four more harnesses. Cline, OpenClaw, Goose and Gemini CLI each had a prompt-time channel deja was not using, so they were getting the session's opening context no matter what was typed next. (#1227)
- Work records for Kimi Code and Copilot: the commands they ran and the files they changed are indexed, not just what was said. Both file that work outside the message stream, so it was reachable from nothing. (#1221, #1231)
- Antigravity's CLI sessions carry a project. Its conversation metadata is written by the IDE, so every `agy` session parsed without one — and a session with no project is invisible to recall, which ranks within the project you are in. (#1227)
- A benchmark for the first minute: install onto history you already have, and measure time to first correct answer, how much of that history was reachable, and hit@1/hit@5 on questions only it can answer. (#1215)

### Changed
- Guidance is a skill rather than a block of text that sits in context all session. Eight harnesses — Cursor, Codex, Gemini CLI, Qwen Code, Kimi Code, Goose, OpenClaw and Roo Code — read one shared skill at `~/.agents/skills/`; Claude Code, opencode, Antigravity, Copilot, pi, Hermes and Cline each read their own place. Old blocks in `AGENTS.md`, `GEMINI.md`, `QWEN.md` and Roo's rules file are removed on the next install, and so are the per-harness skills an earlier version wrote.
- A plainly worded question no longer excludes its own answer. When requiring every content word leaves only a handful of sessions, relevance ranking is hung underneath, and `found@50` went from 28/40 to 34/40 on a 1910-session corpus with `hit@1` unchanged. (#1226)
- Before running a command deja has seen before, the line now says what happened last time rather than how often it was run. (#1228)
- Redaction is faster on ordinary prose, transcripts are read with a pooled buffer, and the co-occurrence pass interns its tokens: peak memory on a full build drops from 619MB to 387MB. (#1226)

### Fixed
- You are told once per session rather than once per machine. The notice that deja recalled something was rate-limited against the index, so with several agents open only the first one said anything while all of them were being helped. (#1228)
- A filesystem path is no longer redacted as a secret. A macOS scratch directory supplies both the case mix and the entropy the heuristic looked for, so a record that was nothing but a path was destroyed rather than masked. (#1233)
- Imported sessions are indexed the way ingested ones are. Sync skipped date tokens, the tool bit and the tool-output cut, so a synced store could not answer "what did we do in may" and spent its budget differently from a rebuilt one. (#1223)
- A recalled session is quoted at the line that matched, not the line it opened with, and receipts no longer stutter after the host's own prefix. (#1228)
- `deja install --auto` no longer leaves Gemini, Qwen, Kimi and Cline without their MCP server; installing twice leaves what installing once left; a config reached through a symlink is written through rather than replaced; a CRLF config no longer gains a second `extensions:` key; and `deja doctor` prints its table on a machine without Claude Code. (#1227)
- The date column holds one form per list, so two sessions a day apart no longer read as "6d ago" and "Jul 26". (#1232)
- Windows CJK build cost is down without changing a byte of the index. (#1220)


## [0.17.0] - 2026-08-11

This release moves deja from finding the right past session to putting its
decision in front of the agent when it matters — before an edit or a command, at
the start of a session, and inside the recall itself — and ranks by what actually
held rather than what merely matched.

### Added
- Recall at the point of an action: before an agent edits a file or runs a command, deja names that file's or command's prior decision, not just that history exists. It is wired into `PreToolUse` for codex and Claude, and reads codex's `apply_patch` edits, not only `Edit`/`Write`. A measured A/B on a real agent settled the wording — a bare pointer to `blame` changed nothing it did, the decision itself drove it to reuse the earlier fix. (#1153, #1163)
- A project's settled decisions at session start: the accepted notes promoted for the current project are injected up front, independent of the query, so the agent follows what was decided instead of re-deciding it. Trust-policy gated and capped. (#1160)
- `how` reports how this machine actually runs a thing — the real command with the real flags, from what agents have run before — so a build, test or deploy invocation is reused rather than guessed. (#1154)
- Recall matches by error signature when nothing matches exactly, and pairs an error with the command that followed it, so hitting a failure surfaces what cleared it last time. (#1149, #1151)
- Recall says what the best session concluded, not only where the query words appear, and the credit line names the session that earned a reuse. (#1145, #1148)
- A hit says when its session backed an approach out, and `deja` offers to record a dead end in the sentence that reports it. (#1152, #1150)
- An off-by-default plan check, with the harness to re-measure it. (#1125)

### Changed
- Outcome-aware ranking: a session whose own text says it reverted an approach and reached no other conclusion ranks below one that held, while a session that reverted one thing and settled another keeps its place. (#1159)
- Reuse reaches past dead ties: a recurring answer a louder session out-matches by a couple of terms is still surfaced by how often it was pulled back, and a heavily-reused near-miss still loses to a strong match. (#1164)
- Ranking weighs how much of a session matched against how long it is, sizes the unprompted recall to the strength of the match, and snippets show where the query terms meet — a wider window, strongest matches first. (#1155, #1146, #1140, #1142, #1143)

### Fixed
- Secrets named in languages other than English are redacted. (#1144)
- `resume` obeys the trust policy; a re-run `import` keeps a session's earlier touched files; `forget --unforget --dry-run` no longer restores. (#1131, #1133, #1130)
- The project and id in the injected context header are sanitized; a notes append starts a fresh line when the file has no trailing newline; an invalid `--re` names the pattern you typed rather than deja's injected prefix; the stats card replaces an extension it cannot honour instead of doubling it. (#1156, #1132, #1161, #1162)
- The test suite no longer writes to the developer's real notes store. (#1158)

### Performance
- A full index build spills postings to disk instead of holding them all in memory, and co-occurrence pairs are stored once rather than in both directions. (#1136, #1137)

## [0.16.9] - 2026-08-09

Another audit pass, weighted toward two failures that lose data rather than
just misreport it. A named pipe or socket sitting in a scanned session store
froze `deja index` for good — the parser's Open blocks on a pipe with no writer
and never returns. And `import` on a machine that had never indexed recorded
every local transcript as already seen while ingesting none, so the reader's
own history stayed invisible until a full rebuild.

### Fixed
- `index` skips FIFOs, sockets and other non-regular files across every discovery path instead of blocking forever on the first one it meets. (#1128)
- `import` before the first `index` no longer hides local sessions: the initial manifest starts with an empty file set, so the next index ingests them next to the imported records. (#1128)
- The semantic search tier is scoped by the trust policy like the lexical tier already was — an imported peer's withheld content no longer surfaces through the embedding fallback in `search`, MCP `recall` or `recall_context`. (#1128)
- claude timestamps parse a stringified or fractional epoch and a zone-less or seconds-less RFC3339 instead of losing the turn's date and sorting it as "-". (#1128)
- An identical re-promote no longer grows the note or lifts its recall weight; `promote` and `remember` say when the note they wrote is still tombstoned; the shared-id hint names `harness:id` instead of a `--harness` flag those commands reject; `search` honours `--` as end-of-options and says when results were capped; MCP `remember` accepts tags. (#1128)

## [0.16.8] - 2026-08-07

A long audit pass. Most of it was one recurring shape: a surface that knew a
fact and did not say it — the same state described three different ways across
`last`, `search` and `stats`; a rule applied on one path and skipped on its
neighbour; advice that named something you could not run. A handful were worse
than untidy: a project name could carry terminal control sequences or an HTML
event handler through to a screen, and a peer-supplied field could forge an
entry that read as local.

The index format changed, so the first run after upgrading rebuilds it once.

### Added
- A Postgres-backed Hermes store is indexed when `DEJA_HERMES_PG_DSN` is set. Hermes can keep its sessions in PostgreSQL instead of SQLite, and deja only globbed `state.db`, so the whole harness went dark after the cutover. It reads the same query over `psql` and re-reads incrementally by timestamp. (#1018)
- `deja <command> --help` prints that command's own syntax. It was rejected as an unknown flag everywhere, and `mcp --help` went as far as starting the server. (#1111)

### Fixed
- Six surfaces printed raw terminal control sequences from session text: `last` could blank the screen and `show` could overwrite what was already there. Display now goes through one shared safe-output rule. (#1083)
- `stats --html` no longer lets a project name inject an HTML event handler, and a peer-controlled `project` can no longer forge a result entry without its `imported:` prefix — both travelled by sync. (#1075, #1080)
- The trust policy is applied before the result cap, not after: a denied origin used to empty the results while the top hit still read at full confidence. (#1060)
- A rebuild keeps imported note state, so a retracted decision no longer reads as accepted afterwards. (#1049)
- `doctor` calls an unreadable index damaged instead of "built, up to date", says when a rebuild happened because the store could not be read, and stops calling an uninstalled harness "unplugged". (#1108, #1110)
- `sync export` into a file explains what it wanted instead of handing back a raw `mkdir` error, matching the import side. (#1112)
- `install`/`uninstall` write the codex SessionStart hook to `CODEX_HOME` (via `sources.CodexHome()`) instead of a raw `~/.codex`, so a sandboxed install stays sandboxed and a non-default codex home gets its hooks where codex reads them. Every other codex path already honoured it. (#850)
- A round of smaller honesty fixes across `doctor`, `ctx`, `statusline`, `brief`, the MCP surfaces and the help text — each a surface stating one thing while another stated its opposite. (#1034, #1071, #1101, #1102, #1103, #1106, #1107)

## [0.16.7] - 2026-08-03

Two weeks spent on the trust policy and on the states a machine gets into when
a disk goes away. The policy turned out to be honoured by search and by nothing
else: the listing, the handoff picker and the block printed after an import all
read straight from the index. Separately, an index whose postings were gone
answered "no matches" about text it still held, and every surface called it
healthy.

The index format is unchanged, so upgrading does not rebuild.

### Fixed
- The trust policy now covers every path that chooses for you: `deja last`, `handoff` with no id, and the proof block printed after `sync import`. Search was the only one applying it. (#937, #951, #953)
- `doctor` reports the policy in force rather than the file on disk — with no policy file and `DEJA_AUTORECALL_LOCAL_ONLY=1` set, it said every origin activates everywhere. (#939)
- A rule deja never consults is no longer summarised as if it were in force, and an `imported:<group>` rule that matches nothing in the index is named. The part after the colon is the source project's first path component, not a machine name; the vocabulary said peer. (#941, #955)
- The session-start receipt says when the policy withheld memory from that session instead of only naming the policy. (#948)
- An index whose `buckets/` directory is empty counts as damaged: search rebuilds instead of answering "no matches" about text still in the record log, and `doctor` stops calling it built. (#946)
- An unplugged disk is not a permissions problem: `search` and `doctor` name the vanished mount point, and a store on an ejected volume is called unplugged rather than missing. (#931, #933)
- Promoted notes keep their corrections newest-first after an incremental build; the note led with the answer that had been overturned. (#944)
- Note buckets are dated in the reader's zone, and `doctor` says when a zone change regrouped the days. (#911, #935)
- `forget` names what it is dropping: a day of `remember` notes is no longer reported as a promoted note, and `forget --list` names the way back. (#919, #957)
- The hook reports a running build on machines that have environment facts, names a store it could not read, and its background refresh reindexes before recomputing the digest. (#913, #917, #927)
- The first seconds after install are not described as a quiet day or a missing index. (#925)
- A session id is accepted the way it is pasted — quotes, backticks, stray spaces, and the `harness:id` form deja prints itself. (#921)
- `ctx` says when an elided id reached more than one session. (#923)
- `sync import` reports how many sessions arrived and names a few, instead of a record count. (#929)
- `deja update` names the package each manager actually ships; `npm update -g deja-vu` was a 404. (#915)
- Dumb terminals get no colour, and a read-only cache directory no longer stops `search` answering from the index that is there. (#903, #904)

## [0.16.6] - 2026-08-02

Two weeks of reading deja's own output on states it had never been run in: no
sqlite3, no git, a locked store, a full disk, a killed rebuild, a machine with
no history at all. Most of what came back was deja knowing something and not
saying it.

The index format is now 22, so the first run after upgrading rebuilds. Titles
derived under the older rules were being carried forward untouched, and a
rebuild is the only way to re-derive them.

### Added
- `deja friction` — the errors that recur across sessions, read in one pass over the record log. (#622, #624, #626)
- The first screen names the memory agents keep returning to, and the statusline says what earlier sessions decided about the file in hand. (#634, #638)
- Codex and Cursor transcripts now yield commands, output, file paths and edits, the way Claude and opencode already did. (#621, #628, #629)
- `deja stats` says what `deja restore` could hand back, before anyone needs it. (#644)
- The session-start block tells the agent what this machine is missing before it trips over it. (#632)

### Fixed
- Forgetting a promoted note only wrote a tombstone: search went quiet while the text stayed in `notes.jsonl`, a file deja wrote. Forgetting the source now also says the note kept its content. (#841)
- `promote --to` wrote a file meant for another person with neither redaction nor the warning `share` and `sync export` both print — a token went out verbatim. `deja view` now names what its page carries too. (#848, #857)
- `stats`, `last`, the MCP resource list and `deja view` dated sessions in UTC while the brief used the reader's zone, so several screens named different days for one session. (#849, #856)
- `forget` and `unforget` did not accept the id a result line prints — the elision is part of what a reader copies, and it appears in no id, so neither the prefix nor the substring match could hit. Every other command already read it. (#853, #855)
- A hook waited for the host to close stdin, which cost seconds per turn on a host that holds the pipe; `hook-antigravity` also treated an unreadable payload as the first turn, which would have injected the digest before every model call. (#846)
- `deja uninstall` left an empty guidance file it had created, that file's backup, and two empty directories; a symlinked `skills/` is left alone. (#840)
- When everything is forgotten, search advised `deja index` and `doctor` — neither can bring back a tombstoned session — instead of naming the forgotten count. (#844)
- `deja index` said nothing when there was nothing to do, and a warmup that found nothing left its sentinel behind. (#824, #839)
- `deja install` with no target named none of the targets it knows, on the first command a new machine runs. (#830)
- A command that landed in the window where a rebuild recreates the index reported a missing `manifest.gob`. (#822)
- The twelve-month chart drew a quarter of an old store as if it were the whole shape. (#854)
- Six documented examples that did not match what deja does, including a `go build` line that fails outright. (#847)
- `deja promote <id> --state accepted` already took back a `rejected` mark; nothing said so. (#845)
- The brief spent a line restating `today` as `this week`, and printed one session title twice. (#842, #843)
- Recall stayed dead after an upgrade: the hooks read an index written by another format version and matched nothing, and neither they nor the digest cache asked for the rebuild that would fix it. The same hole was open for a damaged index. (#777, #800)
- A promoted note served its oldest correction. After a hundred careful corrections the hook handed the agent the first answer as fact; the note now leads with the latest, as its title already did. (#812)
- An interrupted `deja forget --unforget` lost the session: the tombstone was gone, the index did not have it back, and no ordinary command restored it. The tombstone now outlives the rebuild. (#810)
- `forget` reported success when it could not clear the title a note borrowed from the forgotten session — the first turn of that session, on disk, after deja said it was gone. It also now says which peers already have a copy. (#804, #808, #788)
- `doctor` blamed the harness format for a missing `sqlite3` CLI and for a store it had no permission to read, and said nothing at all when only a subdirectory was locked. It now names the cause, and lists `git` alongside `sqlite3`. (#792, #802, #816, #796)
- `deja index` said nothing when there was nothing to do, when it skipped a whole harness for a missing tool, or when a directory refused to be read. (#824, #794, #818)
- Wiring repair only triggered on a version change, so a moved binary left every config pointing at a path that no longer exists. `deja update` now defers to Homebrew, npm, scoop, Nix and winget instead of writing into their trees. (#773, #775)
- Answers that could not be acted on: `try fewer words` for a query whose every word was too short, or for two words that never co-occur (deja now names each word's own count), and query advice on a machine with no history at all — in search, `files`, `ctx` and `restore`. (#828, #826, #832, #834)
- A command that landed in the window where a rebuild recreates the index directory reported a missing `manifest.gob`. (#822)
- Notes without a project, and promoted notes without a state, were dropped at index time — the one class of content deja cannot re-derive from anywhere else. A promoted note with no source session is still dropped, but now counted. (#771, #814)
- Relative dates used the timestamp's zone rather than the reader's, `deja last` printed `0001-01-01` for a session with no time, and the brief's `covering` line started from the earliest *last* activity, hiding the early history of long-running sessions. (#767, #765, #786)
- Denied writes reported syscalls: `deja index`, `promote` and the notes rewrite now say what to change, and the notes rewrite names a full disk as a full disk. (#798, #806, #808)
- Ranking and identity: an imported session no longer outranks an identical local one by accident, two transcripts sharing an id are attributed the same way on every build, and a rejected session is moved below the rest with a line saying so. (#711, #699, #694)

### Changed
- Skipping the CJK scan on bodies that contain no CJK: 118–158 µs down to 5.5 µs per body, byte-identical index. Found and fixed by @AliceLJY. (#640)
- The first screen went from 3.2 s to 0.6 s. (#627)

## [0.16.5] - 2026-07-31

### Added
- `deja files` and `deja restore` build the index instead of hanging silently, the brief shows a question this store has been asked before, and a compacted session is handed back its own evidence. (#617, #590, #588)
- The index is built on install and on a bare first run. (#587)
- Search says when the files a session touched have moved since. (#571)

### Fixed
- `--deep` reported drift on a healthy index; `install` said nothing useful on an unknown target; `forget --dry-run` described work it had not done; MCP `blame` sent whole transcripts to the agent. (#613, #612, #610, #611)

## [0.16.4] - 2026-07-30

### Added
- `deja restore` recovers a span an agent replaced, and `deja files` says which files a topic's work actually touched. (#569, #566)
- Sessions are named after the repository they worked in, and tool calls' file paths and commands are indexed. (#563, #558, #561)
- `deja handoff --to agy` starts Antigravity through its CLI. Thanks to @shgpavel. (#524)

### Fixed
- `deja handoff --to hermes|openclaw|roo` answered `don't know how to hand off` instead of printing the digest to paste. Thanks to @shgpavel. (#524)
- Tool output was labelled as the user. (#560)

## [0.16.3] - 2026-07-30

### Fixed
- Relevance results reported the window instead of the pool they came from: `total: 50, capped: false` however deep the candidate set went. `total` is now the pre-truncation count and `capped` says whether anything was withheld, on every relevance exit including the merged close-tier tail. Found, diagnosed and fixed by @AliceLJY. (#497, #554)
- Harness plumbing was indexed as if a person had said it — `[Request interrupted by user]`, system reminders and slash-command echoes were searchable and counted in stats. 304 records on a real index. Only complete blocks are stripped, so a message that merely mentions a marker keeps its text. (#551, #555)
- The winget version manifest was left at the previous release by `pinmanifests`, so the manifest set failed its own consistency test after every release. (#523)

### Changed
- The comparison page covers the four largest memory projects beyond the six it started with, and no longer claims retroactive indexing is unique to deja — MemPalace mines Claude Code, Codex and Cursor transcripts too. (#310, #525)

### Fixed
- `deja handoff --to hermes|openclaw|roo` answered `don't know how to hand off` instead of printing the digest to paste; the paste path now covers every harness the registry marks paste, kept in sync by the capability drift test. (#524)

## [0.16.2] - 2026-07-29

Ranking learned to tell a conclusion from a conversation about one, and search
stopped reading a session it was never going to rank on.

### Added
- Search output says which tier answered (`exact`, `close`, `stemmed`, `semantic`, `relevance`), how many sessions matched, and whether the cap hid any. Both the text and JSON forms carry it. Thanks to @AliceLJY for the naming. (#494, #495, #496)
- A decision that was later reversed comes back marked — `[this was tried and rejected, 2026-07-29]` with the reason — instead of being served as current truth. (#506)
- `.mcpb` bundles for the registries that install MCP servers from one, with the entry point fixed to a real server rather than a stub. (#484, #487)
- `deja --help` lists the search flags. Thanks to @AliceLJY. (#493)

### Changed
- A session that concluded something now outranks one that only discussed it, and a pasted log ranks below a human answer to the same question. Measured on a benchmark built for it: 8/8 and 8/8, LongMemEval-S unchanged. (#509, #510)
- Reuse counts a déjà vu moment — the user returning to the same ground — alongside agent recalls, and the 1.2× ceiling on it is now covered by a test rather than a judgement. (#511)
- Coverage is measured on shipped packages; benchmark harnesses under `scripts/` no longer count, and the floor rose from 82.0% to 87.5%. (#515)

### Performance
- A query over a common word reads at most 64 matching messages of any one session, sampled across it. `"index"` on a 3.5 GB store: 61.8 ms → 26.9 ms. Nothing under the bound is touched, and no session is ever dropped from the candidates. (#513, #515)
- Records are read in coalesced spans instead of three syscalls apiece. (#504, #512)
- Full rebuild is 12% faster: redaction scans bytes instead of running a regex, and the opencode query stops parsing rows that cannot match. (#498)
- Claude transcripts decode into declared types — a third fewer allocations. (#502)

### Fixed
- The first index built by running a search — which is how nearly everyone builds their first one — reported no progress at all: a spinner reading "starting" and a bar frozen at one notch for the whole build. (#505, #517)
- deja indexed its own recall blocks, so a session could be answered with deja's own earlier answer. (#480, #488)
- Recall returned the question rather than the decision that followed it. (#490, #491)
- `deja-stats.svg`, a generated card from a local run, had been committed to the repo. (#514)
- The front page quoted a search latency measured before two changes to the read path. It is 1.3 ms median and 17 ms on the most common word, both from `scripts/searchbench`; the LongMemEval median search was quoted at 40 ms and is 19 ms. (#519)
- The file-ranking test gave two git calls 400 ms and failed on runners that miss it. (#516, #520)

## [0.16.1] - 2026-07-28

### Added
- MCP bundles: every release now ships an `.mcpb` per platform, so desktop apps that install MCP servers as bundles can install deja by opening one file. The bundle carries the binary — no runtime, no package manager, nothing hosted. Terminal agents keep using `deja install`. (#481, #482)
- A documented machine-readable read contract: `deja last --json`, `deja show --json --harness <name>` with `--offset`/`--limit`, an explicit `--limit` on search, and a `source` field recording whether a session is local or imported. Exact reads require both id and harness, because ids collide across harnesses and a machine reader cannot notice. Thanks to @adamsitar. (#476)

### Fixed
- Windows scoop and winget manifests were pinned to 0.15.6, so those two channels were two releases behind. (#478)
- Renamed the SQL escaping helper to say what it does; the old name had caused two silent query failures in a day. (#475)


## [0.16.0] - 2026-07-28

Every harness that can inject context now recalls on its own, and `deja doctor`
tells you which of your integrations are actually live.

### Added
- Auto-recall in every harness that can inject context, rather than only the ones with a documented hook. (#362)
- Installable from the Claude Code and Codex marketplaces; the same bundle installs into six registries. (#367, #391)
- Hermes Agent sessions are indexed, with a plugin, MCP wiring and support for its flat store layout. (#376, #389)
- pi gets `/deja` and keeps recall stats in its footer; opencode toasts the recall receipt and shows the first build as a moving bar. (#381, #384, #392)
- aider, cline, goose and roo install to real recall: aider re-reads a `read:` context file every message, cline gets a plugin whose rule is built at session start, goose injects through MOIM (which survives compaction) plus an MCP extension, and roo installs into every host it has run in, following `roo-cline.customStoragePath` when set. (#396, #400, #405, #410, #426)
- The plugin bundle ships the MCP server, so the six registries that install Claude-format plugins — Claude Code, Codex, Cursor, Qwen, OpenClaw, Copilot CLI — get recall tools without a second install step. (#394)
- `deja doctor` reports every auto-recall integration as wired, stale or missing, instead of only checking the one you asked about. (#417)
- `deja handoff` starts cline, goose and kimi directly, and hermes sessions resume by id. (#450, #452)
- `deja bench prompt` scores recall per prompt against a per-topic corpus, with negative controls and gated marathon/fresh shapes. (#424)
- Time queries understand month names in English and Russian, resolving to the most recent occurrence. (#456)
- Nightly snapshot builds publish a rolling prerelease from main. (#398)

### Fixed
- Harness wiring that was installed but not working: gemini hooks live in an extension rather than `settings.json` and SessionStart was never hooked, qwen's hook needed a millisecond timeout, kimi hooked the wrong event and could not read prompts sent as parts, codex left an existing entry stale instead of adopting it, and installing twice added a second hook rather than updating the first. (#373, #378, #382, #385, #386, #388)
- opencode recalled only at session start, and lost the transcript to compaction before it was indexed. (#383, #390)
- Auto-recall never fired for CJK prompts, Traditional and Cantonese function words were weighed as content, and Traditional/Simplified recall only worked in one direction. (#368, #369, #370)
- Read paths waited out a rebuild instead of serving the index they already had. (#380)
- `deja uninstall --all` left every hook and plugin in place, pointing at a binary it had just removed. (#421)
- Upgrading deja never repaired wiring an older version had written, so an integration could sit dead for weeks with nothing reporting it. Recorded targets are rewritten on the first session start after a version change. (#431)
- A rewound session kept its old text in the index: the append fast path compared size and mtime, which a rewrite can leave unchanged. It now checks a hash of the prefix it already read. (#454)
- Forgotten sessions came back if `~/.config/deja/tombstones` was lost. A second copy now lives beside the index. (#442)
- `deja goose` and `deja aider` launched an agent when given a one-word search. (#452)
- A harness that changes its database schema made its history vanish silently — all four SQLite parsers reported a failed query as an empty store, and grok's incremental filter had never worked at all. (#474)
- A read-only index refused to answer instead of serving what it already held. (#472)
- A query that happened to name a subcommand ran it. (#430)
- MCP clients that send numeric arguments as strings were rejected. (#423)
- doctor could not find the MCP server if it had been installed under another name, reported a codex hook as trusted when its hash no longer matched, and warned about sessions that were simply unused. (#428, #433, #440)
- Recall skipped long sessions instead of narrowing them, withheld the answer on old sessions rather than just the déjà vu line, and required more terms than most real questions carry. (#424, #438)
- `deja share` under-counted secrets by ignoring those redacted at index time; roo history was filed under cline; a bucket could read past the end of its posting block; an empty index printed nothing at all; `deja stats --card` appended a second `.svg`. (#432, #444, #448, #470, dc3fd84)

### Changed
- Every command spent about 650ms proving the index was fresh. Derived file state is carried forward when size and mtime match: 650ms -> 6ms. `deja doctor` no longer parses a 2.8GB database to answer a question about the newest session — 8s -> under 100ms. (#458, #460)
- Coverage is gated in CI per package, and the contributing bar is 90%. (#462, #463)
- Documented what `deja update` verifies and what it does not. (#446)


## [0.15.7] - 2026-07-25

Existing indexes are rebuilt once on first use: the on-disk format changed
several times in this release.

### Fixed
- Non-ASCII search was scanning the whole vocabulary on every lookup: tokens were sharded by their first two *bytes*, so a prefix plus half a UTF-8 sequence collapsed every Russian, Chinese and Greek token in the corpus into one bucket. Median search on a 100k-passage corpus: Chinese 4.02s -> 1.45s, Russian 1.50s -> 186ms. (#351)
- Russian inflection folding never fired — Cyrillic terms were handed to the ASCII stemmer, which appended English suffixes to them. Folding now covers the third-declension nouns (сеть/сети/сетью, новость/новостей) and short verb stems (знать/знал/знаю), while no longer reaching unrelated words: цель no longer recalls целая, часть no longer recalls час. (#351)
- Cross-machine sync: watermarks are per peer, a message sharing the newest timestamp is no longer skipped, tombstones and the exclude list survive a cache wipe, and a message the harness never stamped can now reach another machine at all. (#346)
- `--harness` and `--project` searches returned nothing when the unfiltered top of the ranking was full of other sessions — the scope was applied after truncation. Session ids could also collide during an incremental update, merging two sessions' postings. (#348)
- Recall kill switch and trust policy now bind every path, including the session-start hook cache. (#347)
- Chinese questions reach the relevance tier: fullwidth punctuation was glued into terms. Question grammar (在哪, 什么) no longer weighs as much as the entity asked about — MIRACL Chinese hit@1 40.4% -> 42.5%. (#345, #360)
- Relevance results no longer render "0 matches" with no snippet when a session surfaced through a folded form. (#352)

### Changed
- Index is smaller: records intern their session key and source path instead of repeating them (a real store wrote 90 distinct paths 57 000 times), large tool output and file dumps are deflated, and bucket directories no longer store an offset the reader can derive. A 1000-session store goes 66 MB -> 53 MB; the saving is larger the more tool output the corpus holds. Index build and search latency are unchanged. (#354, #357, #358)
- Search latency on queries that fall through the ladder: the token catalog is cached between queries instead of being rebuilt by both the stem and fuzzy tiers, and fuzzy matching only compares tokens whose length is within its edit limit rather than every token in the corpus — 168ms -> 36ms per term on a 267k-token vocabulary. (#350, #356, #360)

## [0.15.6] - 2026-07-24

### Added
- CJK bigram indexing (#337, design by @AliceLJY): Chinese/Japanese/Korean text gets first-class exact search and full ranking; index version bumps for one automatic rebuild. Note: a single-character query against a longer run (`茶` in `喝茶`) resolves via the close tier, not exact.
- Goose harness by @syf2211: legacy JSONL sessions and SQLite `sessions.db` (>= 1.10.0), resume via `goose session --resume --session-id` — sixteen agents indexed. ([#255](https://github.com/vshulcz/deja-vu/issues/255))
- Russian: conversational stop words and inflection folding in relevance ranking — сеть meets сетью and сети; instruction glue no longer anchors search or déjà vu.

## [0.15.5] - 2026-07-24

### Added
- `deja view` — browse your memory in one local HTML file: sessions with capped previews and client-side filtering, the verbatim recall audit trail, and curated notes with lifecycle badges. No server, no external assets; the page opens in your browser and nothing leaves the machine. (#334)

### Fixed
- Site: wide tables scroll inside their own container (mobile pages no longer overflow sideways), and the comparison page lost its broken navigation strip. (#333)

## [0.15.4] - 2026-07-24

### Fixed
- Agent startup never waits on digest work: the session-start cache is served at any age and refreshed by a detached process; the cache is also scoped per project directory, so switching projects no longer starts cold. Worst-case session-start on a dirty multi-gigabyte store: ~1s -> under 100ms; opencode startup overhead ~1.8s -> ~0.8s (the remainder is MCP process spawn). (#330)

## [0.15.3] - 2026-07-24

### Added
- Cline harness: both store generations (modern `~/.cline/data/sessions` and the VS Code globalStorage tasks), MCP wiring, resume for modern sessions. (#306)
- Roo Code harness: VS Code globalStorage tasks with per-task metadata. (#307)
- OpenClaw harness: pi-lineage transcripts under `~/.openclaw/agents/*/sessions`, compaction checkpoints and archives skipped; `deja install openclaw` wires `openclaw.json`, doctor gains store and wiring rows. (#312, #320)
- Tags on curated notes (`deja remember --tag`, `deja promote --tag`) and conflict surfacing when an accepted note covers ground another accepted note already holds. (#309)
- Sessions are findable by when they happened: month, year and year-month land in the index as tokens (`deja "what did we do in may"`), and relative-time phrases ("a week ago", "last month") resolve against the moment of the search. (#323, #325)
- Benchmarks report the official per-evidence recall alongside hit@k, and both harnesses ship a JSONL miss report. (#322)

### Changed
- Search ranking: a session is scored by its best message instead of its whole-transcript sum, sessions covering more distinct query words outrank repetition, natural-language queries fall through junk substring intersections to relevance ranking, and stem forms fold in when the exact word is absent from the corpus. LongMemEval-S hit@1 84.9% / hit@5 94.3%; LoCoMo hit@1 69.8% / hit@5 85.6%. (#317, #318, #322, #324)
- Real questions of three or more words answer with a ranked weak tail instead of silence; bare short queries and bare quoted phrases keep the silence contract. (#322)

### Fixed
- Agent startup no longer pays for indexing: the session-start hook ran a full synchronous index through a garnish lookup — up to ~10s per start with a dirty multi-gigabyte store, now ~0.2s worst case. (#315)
- `Ensure` silently ignored its harness scope: every scoped build ingested all stores. (#319)
- Déjà vu matched dotted terms by their first sub-token (an IP degraded to one octet and fired on unrelated sessions); the visible line now names its trigger terms and `deja log` records them. (#313)
- Newer Gemini CLI chats parsed to zero: message state inside `$set` snapshot lines is read now. (#316)
- A missing bucket shard aborted stem-tier searches instead of meaning "no postings". (#323)

## [0.15.2] - 2026-07-23

### Added
- Public benchmark numbers on the site and README; scoop/winget manifests pinned per release. (#304, #305)
- Parallel harness parsing and redaction on cold builds. (#303)

## [0.15.1] - 2026-07-22

### Fixed
- Déjà vu calibration: identifier-shaped terms only, session-level document frequency, cooldown — no more "you have been here" on every prompt. (#292, #293)
- Codex hook could hang agents that keep stdin open; reads now bound at 300ms, and doctor reports codex-side hook state. (#294)

### Changed
- MCP recall serves a stale snapshot instantly while rebuilds run detached; hook and search latency cut across the board. (#291-#302)

## [0.15.0] - 2026-07-21

### Added
- Déjà vu moments: when a prompt matches work your own history already answered, the per-prompt recall now announces it with a visible one-liner — `deja-vu: you have been here — "<that session>" (3w ago)` — and the moment is counted in stats and the weekly numbers.
- Bare `deja` on a terminal shows a living brief instead of help text: today's sessions, recalls served and what they distilled, this week's déjà vu moments, recent sessions, and a suggested search from your own history. `deja help` keeps the usage text.
- The session-start recall receipt now carries the day's tally: how many recalls deja served today and how much history they distilled.

### Fixed
- `deja uninstall` could leave a hook entry with `"hooks": null` in Claude Code's settings.json when the entry carried a matcher — Claude Code then rejected the whole settings file. Uninstall now drops the entry, and any damaged entry from an earlier version heals on the next install.

## [0.14.4] - 2026-07-21

### Added
- The first index greeting now suggests a search phrase taken from your own recent history instead of a generic hint.
- `deja stats` and the statusline show what recall replaced: served bytes against the source transcripts they were distilled from, as a personal ratio measured from real events.
- Search learns from use: sessions that agents keep recalling rank slightly higher (hard-capped at +20%), marked `reused N×` in output and as an additive `reused` JSON field.
- Co-occurrence rescue: on zero results, one query token may swap for a neighbor the corpus itself ties it to — a 245 KB map built at full rebuild, narrated like any variant.
- Compound identifiers answer for their parts: `deja "user profile"` finds `getUserProfile` and `refresh_token_rotation`.
- A reviewed developer-synonym table (k8s↔kubernetes and friends) and Russian suffix folding join the stem tier.

### Changed
- Ranking now weighs term proximity and title matches (both bounded) on top of BM25 and freshness.
- The postings AND considers up to 8 query tokens instead of the 3 longest, so a rare token narrows candidates before the scan.
- `deja blame` human output is colored like search results.

## [0.14.3] - 2026-07-21

### Added
- Kimi Code is the twelfth harness: `wire.jsonl` transcripts are indexed retroactively (streamed assistant turns reconstructed from loop events, mid-stream responses survive incremental indexing), `deja install kimi` wires MCP through `$KIMI_CODE_HOME/mcp.json` plus an `AGENTS.md` guidance file, and `deja resume` reopens sessions via `kimi --session`. Spec contributed by @yearth (#248).

## [0.14.2] - 2026-07-21

### Added
- `deja log` — audit trail of served memory: recent recalls and injections as a table, `--last` prints the exact digest most recently injected (hook, per-prompt, MCP, handoff), from a size-capped snapshot file next to the usage sidecar.
- Entropy redaction: bare high-entropy values in secret-shaped positions (any assignment's value side, including the Telegram `digits:token` shape, or a token alone on its own line) are now stripped at index time. Hex digests, UUIDs, paths and identifiers are excluded; measured no rebuild-time cost.
- Earlier-attempt flags: when an old session and a newer one from the same project match the same ground, the older hit is labeled with the newer session's date in CLI output and MCP digests, plus an additive `superseded` field in `--json`.

### Fixed
- Natural-language queries no longer die on the AND: suffix stemming works in both directions (`failing` finds `fails`), short tokens get plural forms, and up to two tokens that no session can satisfy are dropped with explicit narration instead of returning zero results.

### Changed
- MCP recall digests show a relative age next to each session date.
- `stats --card` renders in the site's terminal look: rewind-loop mark and wordmark in the header, scanline texture, accent punchline.

## [0.14.1] - 2026-07-21

### Changed
- Internal restructuring: index engine split into ingest/retrieval/manifest/store-IO files, stats and share/handoff digest building moved to internal packages, table-driven subcommand dispatch, index directory resolved once at startup. No user-facing behavior change.

## [0.14.0] - 2026-07-21

### Added
- Handoff/resume commands corrected against the real installed CLIs: cursor handoff uses positional prompt (no `chat` subcommand exists), pi resume uses `--session`, and grok is marked non-resumable (it has no session flags).
- Per-prompt recall (UserPromptSubmit) now ranks THIS project's sessions by IDF-weighted overlap with the prompt instead of reconstructing an AND query — natural prompts are full of filler that poisoned the old query builder into empty or wrong hits. Excludes the current/too-fresh sessions, dedupes per agent session, and appends a ready citation line.
- `deja stats` counts how often agents actually said "deja-vu recalled" — a telemetry-free measure of memory credited aloud, closed by deja re-indexing those transcripts.
- Ingestion health: malformed JSONL lines and failed file parses are counted per harness, persisted in the manifest, and surfaced in `deja doctor` (details in `--json`). Tolerated loss now leaves evidence instead of disappearing.
- Harness capability matrix (MCP / auto-recall / resume / handoff / prerequisites) generated from the format registry into README and the site; a conformance test pins the published matrix to actual code behavior.
- `deja resume` reopens Copilot CLI sessions (`copilot --resume=<id>`).
- Copilot CLI is now a full MCP target: `deja install` writes `~/.copilot/mcp-config.json` (verified live — Copilot calls deja's recall over MCP), replacing the guidance-only stub.

### Changed
- Handoff digests now end with a pull pointer (source session id + how to recall deeper), turning a lossy one-shot push into push+pull; `deja stats` counts sessions started from a handoff.
- The weekly recall headline counts only agent-initiated, non-empty recalls; auto-injections are reported separately. The recall receipt fires only when the recalled set changed, not on every session start.

### Fixed
- SSH sync push is acknowledged delivery: export watermarks advance only after the remote import succeeds, so a failed transfer no longer silently drops that batch from every later push. Pull failures after the remote export now print the exact recovery command.
- One malformed session file no longer aborts a full rebuild or an incremental pass: parsers are panic-guarded per file and per harness.
- Crash-hardening for in-place index writes (#181): bucket files are replaced atomically, the record log is fsynced before the manifest stamps its size, an uncommitted record tail now triggers a rebuild instead of silently duplicating messages, and full rebuilds keep the previous index recoverable through the rename window.
- MCP recall/blame accept a fractional `limit`: a client that serializes `5` as `5.0` used to get a `-32602` error and no results at all.
- MCP server hardening: large JSON-RPC ids are echoed back exactly instead of being rounded through float64, and a single oversized frame is skipped rather than tearing down the whole stdio session.
- `manifest.gob`/`sessions.gob` are fsynced before the rename, closing a crash window that could leave a torn manifest even though the rest of the index writes durably.
- A session file caught mid-write (torn first line) is fully re-indexed on the next pass instead of resuming an append mid-line, so its first message is no longer dropped.
- `deja install` writes the Windows `cmd /c` shim for Codex and Grok `config.toml`, matching the JSON-based installers.

### Added
- Recall receipt: when auto-recall injects real context, the SessionStart hook now surfaces a one-line notice ("deja: recalled N prior sessions…") instead of working silently; `deja stats` and the statusline report the trailing-week recall count and re-used context volume.
- GitHub Copilot CLI as the eleventh harness: sessions in `~/.copilot/session-state` are discovered, parsed and incrementally indexed; `copilot` is also a handoff target.
- `deja handoff --to <agent> [id-prefix] [--exec]` — package the live context of a session (problem, conclusions, where it stopped) and continue it in a different agent. Composable: `codex "$(deja handoff --to codex)"`; `--exec` launches the target directly. Targets: claude, codex, opencode, gemini, qwen, aider, pi, grok.
- Shareable stats card rebuilt around a trailing-year activity grid with a personal headline; `stats --card` now runs quietly and prints a paste-ready snippet.

## [0.13.1] - 2026-07-19

### Added
- Semantic search fallback: on zero lexical results with a current embedding sidecar, the query is vector-searched against it; results carry a semantic flag.
- Confidence tiers on every hit — exact, close (with the matched variant), semantic (with the cosine) — across CLI, JSON and MCP output.
- Natural-language queries: query-time stop-word dropping and a morphological fallback, so the README's own example phrasing finds its session.
- PreCompact capture and post-compaction re-injection: the transcript is indexed before Claude Code compacts, and the SessionStart digest re-anchors the model afterwards with visible per-session provenance.
- Onboarding builds memory on install: install --auto/--all index detected stores on the spot, the SessionStart hook warms a missing index in the background, and install.sh offers a PATH line.
- Personal headline metrics in stats and an embeddable SVG card (counts only, no project names) with a ready-to-paste markdown snippet.
- MCP tool descriptions rewritten around user trigger phrases, with read-only annotations; the injected digest opens with an actionable line.
- deja bench context: a seeded, ablation-armed context-readiness experiment with coverage gates and negative controls.
- A Dockerfile for directory checkers, and automatic publication of server.json to the MCP registry on release.
- pi (pi.dev) as the tenth supported harness (contributed by @maxandersen).

### Fixed
- Incremental update no longer drops untouched Cursor sessions from the index.
- The search/MCP build path dedups messages, so a session present in two stores is not double-indexed.
- `deja forget` writes tombstones before rebuilding so a crash cannot resurrect forgotten sessions; `unforget` matches by id-prefix so a bare letter cannot revive whole harnesses.
- The aider parser reads unbounded lines, so a multi-megabyte pasted blob no longer drops later sessions.
- Fuzzy and word-form hits rank by relevance (BM25) instead of recency only; stop words no longer over-constrain natural-language queries.
- Install writes the MCP command through `cmd /c` on Windows so stdio clients can spawn it.
- `recall_context` no longer returns a header-only digest for multi-word queries (community contribution).
- A corrupt record length prefix is rejected instead of allocating gigabytes.

### Security
- Redaction now covers HTTP Basic auth, `scheme://:password@host` URLs and PGP armored keys, runs before the size cap so a boundary-straddling secret is not stored raw, and the `sk-`/`xai-` rules no longer destroy kebab-case prose (the xai- fix is a community contribution).

## [0.13.0] - 2026-07-19

### Added
- BM25 ranking with a user-message boost, quoted phrase queries, and a typo fallback that only runs on zero results.
- Optional semantic recall: `deja embed` builds a vector sidecar from a local Ollama/LM Studio endpoint; search and MCP recall blend it with the lexical score.
- `deja blame <path>` and an MCP `blame` tool: sessions that discussed a file, newest and most specific first.
- `deja remember` and an MCP `remember` tool: durable notes stored as a tenth source with full redaction, sync and provenance.
- Privacy set: `deja forget` with persistent tombstones, ingest exclusion patterns, and `deja stats --redaction` per-rule reports.
- `deja stats --card` (shareable SVG) and `deja stats --html` (self-contained, metadata-only timeline).
- Qwen Code as the ninth harness, `deja last --project/--harness` filters, a session format registry with conformance fixtures, and a reproducible `deja bench recall`.
- User-level agent guidance written by install for Claude Code, Codex, Gemini, opencode, Antigravity, Qwen and Copilot.

### Fixed
- Torn lines longer than one scan window no longer lose messages.
- `GROK_HOME`/`DEJA_GROK_ROOT` split: session-read overrides no longer move where install writes config.

### Security
- Index and usage files are created owner-only; install backups and new agent configs are 0600.
- `deja resume` refuses session ids with shell-unsafe characters.
- Agent-facing recall output is framed as untrusted historical data.

## [0.12.0] - 2026-07-17

See the release notes: harness coverage through Grok Build, `deja update`, signed checksums, npm and install-script distribution.

## [0.11.0] - 2026-07-16

See the release notes.

## [0.10.0] - 2026-07-16

See the release notes: Antigravity harness, share redaction hardening.

## [0.9.2] - 2026-07-16

### Added
- MCP install targets for Cursor (~/.cursor/mcp.json), Gemini CLI (~/.gemini/settings.json) and Antigravity (~/.gemini/config/mcp_config.json); install --all picks them up.

### Fixed
- Cursor searches no longer re-merge the whole index on every call: the state store carries a watermark and incremental passes fetch only new messages (#72).
- The same chat arriving from two stores (gemini .json/.jsonl, cursor multi-store) no longer duplicates messages.
- Sync import keeps messages that share a timestamp within one session.
- deja sources lists all seven harnesses and attributes opencode redaction counts correctly.
- deja resume covers all seven harnesses — native commands where they exist, honest guidance where they do not.
- deja stats aligns and colors every harness tag.

## [0.9.1] - 2026-07-16

### Added
- Antigravity support: transcripts are read from the plaintext per-conversation logs, so history stays searchable even where its conversation db is encrypted. Seven harnesses now feed one index.

## [0.9.0] - 2026-07-16

### Added
- Three new harnesses: Cursor (IDE chats from state.vscdb plus CLI agent transcripts), Gemini CLI (both storage generations, including $rewindTo replay) and aider (markdown chat history with fence-aware parsing). deja now indexes six coding agents into one memory.
- `DEJA_AUTORECALL_LOCAL_ONLY=1` keeps synced sessions out of session-start auto-recall.

## [0.8.0] - 2026-07-16

### Added
- deja resume <id-prefix> reopens a found session in its native harness (claude --resume, codex resume, opencode -s), recovering the original working directory where possible. --exec runs it directly.

### Changed
- Subagent transcripts are skipped by default; DEJA_INCLUDE_SUBAGENTS=1 opts back in.

### Fixed
- A session file caught mid-write no longer loses its torn tail line: appends resume from the last complete line and pick the message up exactly once.

## [0.7.0] - 2026-07-16

### Added
- `deja statusline` — one line for your status bar: recalls served to agents today and how much context that was. `deja install statusline` wires it into Claude Code without touching an existing statusline.
- Session-start auto-recall for Codex (hooks.json) and opencode (generated plugin). `deja install --auto` now covers every harness it finds.
- `deja hook-context --plain` prints the bare digest for hosts that inject raw text.

## [0.6.0] - 2026-07-14

### Added
- `deja sync ssh <host>` — one-command sync between machines over system ssh/scp, `--pull` for the reverse direction.
- `deja sync export --full` re-exports everything regardless of watermarks, for onboarding a new machine.
- `deja warmup` builds the index without searching.
- `deja sources` warns when the sqlite3 CLI is missing instead of silently showing zero opencode sessions.

### Fixed
- Sessions replaced during a non-append index update kept stale posting ordinals and dropped out of search.
- A bucket file corrupted by a crash now triggers one automatic rebuild instead of erroring until a manual `--rebuild`.
- Claude project names resolve against the filesystem, so `deja-vu` no longer displays as `deja/vu`.

## [0.5.2] - 2026-07-14

### Fixed
- Sync-imported records survive full rebuilds and incremental index updates; re-import stays idempotent.
- Redaction bookkeeping no longer creates phantom source-file entries that purged imported records.
- Exports skip imported records, so bidirectional sync does not echo history back to its origin.
- The first record of a sync import got a wrong posting offset and was unsearchable.

## [0.5.1] - 2026-07-14

### Changed
- `deja share` filters pasted JSON, diff and CLI dumps out of digests.
- Session titles and stats skip tool-wrapper and caveat noise.

## [0.5.0] - 2026-07-14

### Added

- `deja share <id-prefix>` sanitized markdown session digests for handing context to colleagues.
- `deja sync export <dir>` and `deja sync import <dir>` append-only JSONL batches with export watermarks and idempotent imported-session ingest.

## [0.4.0] - 2026-07-14

### Added

- `deja stats` shareable indexed-work summary with totals, harness breakdown, top projects, 12-month activity sparkline, date range, longest session, busiest day, and `--json` output.

## [0.3.0] - 2026-07-14

### Added

- Optional Claude Code auto-recall via `deja install --auto`, which installs the MCP server and a read-only `SessionStart` hook that injects a capped project-session digest from the warm local index.

## [0.2.0] - 2026-07-14

### Added

- Secret redaction at ingest before records are written to the local index, with manifest counters and `deja sources` redaction totals.

## [0.1.1] - 2026-07-14

### Fixed

- Session ranking lost results for sessions present in two stores.
- The sqlite3 CLI could create a stray opencode.db side-effect file.
- Substring queries (code finds opencode) work through the index again.
- Switching --harness no longer rebuilds the whole index.
- Multi-word snippets anchor and highlight correctly.

### Changed

- Binary index format; warm search 7-9 ms typical.
- Releases publish to GitHub, Homebrew and npm from one tag.

## [0.1.0] - 2026-07-14

### Added

- Local search across Claude Code, Codex CLI, and opencode histories.
- Incremental on-disk index for fast repeated search.
- `deja ctx` compact context output for the best matching session.
- Stdio MCP memory server with `recall` and `recall_context` tools.
- Idempotent installers for claude-code, codex, and opencode MCP config.

[Unreleased]: https://github.com/vshulcz/deja-vu/compare/v0.21.2...HEAD
[0.21.2]: https://github.com/vshulcz/deja-vu/compare/v0.21.1...v0.21.2
[0.21.1]: https://github.com/vshulcz/deja-vu/compare/v0.21.0...v0.21.1
[0.21.0]: https://github.com/vshulcz/deja-vu/compare/v0.20.2...v0.21.0
[0.20.2]: https://github.com/vshulcz/deja-vu/compare/v0.20.1...v0.20.2
[0.20.1]: https://github.com/vshulcz/deja-vu/compare/v0.20.0...v0.20.1
[0.20.0]: https://github.com/vshulcz/deja-vu/compare/v0.19.5...v0.20.0
[0.19.5]: https://github.com/vshulcz/deja-vu/compare/v0.19.4...v0.19.5
[0.19.4]: https://github.com/vshulcz/deja-vu/compare/v0.19.3...v0.19.4
[0.19.3]: https://github.com/vshulcz/deja-vu/compare/v0.19.2...v0.19.3
[0.19.2]: https://github.com/vshulcz/deja-vu/compare/v0.19.1...v0.19.2
[0.19.1]: https://github.com/vshulcz/deja-vu/compare/v0.19.0...v0.19.1
[0.19.0]: https://github.com/vshulcz/deja-vu/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/vshulcz/deja-vu/compare/v0.17.3...v0.18.0
[0.17.3]: https://github.com/vshulcz/deja-vu/compare/v0.17.2...v0.17.3
[0.17.2]: https://github.com/vshulcz/deja-vu/compare/v0.17.1...v0.17.2
[0.17.1]: https://github.com/vshulcz/deja-vu/compare/v0.17.0...v0.17.1
[0.17.0]: https://github.com/vshulcz/deja-vu/compare/v0.16.9...v0.17.0
[0.16.9]: https://github.com/vshulcz/deja-vu/compare/v0.16.8...v0.16.9
[0.16.8]: https://github.com/vshulcz/deja-vu/compare/v0.16.7...v0.16.8
[0.16.7]: https://github.com/vshulcz/deja-vu/compare/v0.16.6...v0.16.7
[0.16.6]: https://github.com/vshulcz/deja-vu/compare/v0.16.5...v0.16.6
[0.16.5]: https://github.com/vshulcz/deja-vu/compare/v0.16.4...v0.16.5
[0.16.4]: https://github.com/vshulcz/deja-vu/compare/v0.16.3...v0.16.4
[0.16.3]: https://github.com/vshulcz/deja-vu/compare/v0.16.2...v0.16.3
[0.16.2]: https://github.com/vshulcz/deja-vu/compare/v0.16.1...v0.16.2
[0.16.1]: https://github.com/vshulcz/deja-vu/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/vshulcz/deja-vu/compare/v0.15.7...v0.16.0
[0.15.7]: https://github.com/vshulcz/deja-vu/compare/v0.15.6...v0.15.7
[0.15.6]: https://github.com/vshulcz/deja-vu/compare/v0.15.5...v0.15.6
[0.15.5]: https://github.com/vshulcz/deja-vu/compare/v0.15.4...v0.15.5
[0.15.4]: https://github.com/vshulcz/deja-vu/compare/v0.15.3...v0.15.4
[0.15.3]: https://github.com/vshulcz/deja-vu/compare/v0.15.2...v0.15.3
[0.15.2]: https://github.com/vshulcz/deja-vu/compare/v0.15.1...v0.15.2
[0.15.1]: https://github.com/vshulcz/deja-vu/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/vshulcz/deja-vu/compare/v0.14.4...v0.15.0
[0.14.4]: https://github.com/vshulcz/deja-vu/compare/v0.14.3...v0.14.4
[0.14.3]: https://github.com/vshulcz/deja-vu/compare/v0.14.2...v0.14.3
[0.14.2]: https://github.com/vshulcz/deja-vu/compare/v0.14.1...v0.14.2
[0.14.1]: https://github.com/vshulcz/deja-vu/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/vshulcz/deja-vu/compare/v0.13.1...v0.14.0
[0.13.1]: https://github.com/vshulcz/deja-vu/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/vshulcz/deja-vu/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/vshulcz/deja-vu/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/vshulcz/deja-vu/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/vshulcz/deja-vu/compare/v0.9.2...v0.10.0
[0.9.2]: https://github.com/vshulcz/deja-vu/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/vshulcz/deja-vu/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/vshulcz/deja-vu/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/vshulcz/deja-vu/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/vshulcz/deja-vu/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/vshulcz/deja-vu/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/vshulcz/deja-vu/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/vshulcz/deja-vu/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/vshulcz/deja-vu/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/vshulcz/deja-vu/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/vshulcz/deja-vu/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/vshulcz/deja-vu/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/vshulcz/deja-vu/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vshulcz/deja-vu/releases/tag/v0.1.0
