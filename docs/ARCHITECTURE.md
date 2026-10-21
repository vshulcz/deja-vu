# Architecture

This document is for people changing `deja` internals.

## Source parsers

Parsers live in `internal/sources` and return `[]model.Session`. The table is
what the loader registers: the forty-two coding agents plus deja's own notes,
which is what `deja sources` prints. `docs/registry/` describes each store's
layout in detail, and `internal/sources/registry_test.go` checks that index
against the loader list.

| Source | Code | Input |
| --- | --- | --- |
| Claude Code | `claude.go`, `claude_history.go` | JSONL files under `~/.claude/projects`, plus the prompts in `~/.claude/history.jsonl` of sessions whose transcript is gone |
| Codex CLI | `codex.go` | rollout JSONL files (`.jsonl.zst` once Codex compresses them) plus `history.jsonl` under `~/.codex` |
| opencode | `opencode.go`, `opencode_v2.go`, `opencode_diff.go` | SQLite database at `~/.local/share/opencode/opencode.db` (the 1.x tables and the 2.0 `session_v2`/`session_message` ones), plus `storage/session_diff/*.json` beside it |
| aider | `aider.go` | `.aider.chat.history.md` files under configured project roots |
| Amp | `amp.go` | one JSON thread object per `~/.local/share/amp/threads/*.json` |
| Gemini CLI | `gemini.go` | JSON and JSONL chats under `~/.gemini/tmp` |
| Cursor | `cursor.go` | SQLite state stores plus CLI agent transcripts |
| Antigravity | `antigravity.go` | JSONL transcripts under `~/.gemini/antigravity*` |
| Grok Build | `grok.go` | ACP update streams and session summaries under `~/.grok/sessions` |
| Qwen Code | `qwen.go` | JSONL chats under `~/.qwen/projects/*/chats` |
| pi | `pi.go` | JSONL transcripts under `~/.pi/agent/sessions` |
| Copilot CLI | `copilot.go` | `events.jsonl` per session under `~/.copilot/session-state`; a `task` sub-agent's records (stamped `agentId`) read as a side chain of the session, a `/fork` as a fork of the session its `session.info` note names |
| VS Code Copilot Chat | `copilot_chat.go`, `copilot_agent.go` | `.jsonl` / `.json` under VS Code User `workspaceStorage/*/chatSessions`, and the extension's own `GitHub.copilot-chat/transcripts/*.jsonl` |
| Cline | `cline.go` | task JSON under the VS Code extension's storage, both store generations |
| Continue | `continuedev.go` | one JSON document per session under `~/.continue/sessions`, with `sessions.json` as the list |
| Roo Code | `roo.go` | task JSON under `rooveterinaryinc.roo-cline` in VS Code globalStorage, and the CLI's own store under `~/.vscode-mock/global-storage` |
| Goose | `goose.go`, `goose_lineage.go` | legacy JSONL sessions and the newer SQLite session store; a `sub_agent` row with `parent_session_id` read as its parent's child, a fork found by the message ids it copied and read as its own turns |
| Kimi Code | `kimi.go` | per-agent `wire.jsonl` under `~/.kimi-code/sessions` |
| OpenClaw | `openclaw.go` | append-only pi-format JSONL under `~/.openclaw/agents` |
| omp (Oh My Pi) | `omp.go` | JSONL transcripts under `~/.omp/agent/sessions` and each profile beside it |
| prime-agent (PrimeIntellect) | `prime.go` | JSONL transcripts under `~/.prime/agent/sessions` |
| DeepSeek Harness | `deepseek.go` | zstd-compressed session JSONL under `~/.dsh/sessions` |
| CodeWhale | `codewhale.go` | one JSON document per session under `${CODEWHALE_HOME:-~/.codewhale}/sessions`, and the pre-rebrand `~/.deepseek` root |
| Junie | `junie.go`, `aui.go` | one `events.jsonl` per session under `${JUNIE_HOME:-~/.junie}/sessions`, blocks folded by step |
| JetBrains AI Assistant | `jetbrains.go`, `aui.go` | the chats in each IDE's `workspace/*.xml`, agent chats in `aia-task-history/*.events` |
| Windsurf / Devin Desktop | `windsurf.go` | one AES-GCM sealed `CortexTrajectory` protobuf per Cascade conversation under `~/.codeium/windsurf/cascade` |
| CodeBuddy Code | `codebuddy.go` | JSONL per session under `${CODEBUDDY_CONFIG_DIR:-~/.codebuddy}/projects/<cwd>/`, OpenAI Responses-style items; WorkBuddy's `~/.workbuddy` too |
| Reasonix | `reasonix.go`, `reasonix_stores.go`, `reasonix_v4.go` | under `~/.reasonix` (`%APPDATA%\reasonix` on Windows): flat role/content JSONL in `sessions/` and `projects/<slug>/sessions/`, and the 1.x session directories (`sessions-v4/<id>/`, `desktop-sessions-v5/by-id/<id>/`) whose `events.frames` log is zstd-framed JSON |
| TRAE CLI | `trae.go`, `codex.go` | Codex rollouts under `${TRAE_HOME:-~/.trae}/cli` (`sessions/`, `archived_sessions/`, `history.jsonl`); user turns from `user_message` and `item_completed` events only, tool calls from `history_mutation` |
| Muse Code | `muse.go` | one event-sourced JSONL per session under `${XDG_DATA_HOME:-~/.local/share}/muse/sessions/YYYY/MM/DD/<id>/`, `retained_frame` children unwrapped; subagent logs under `subagent/` as task, edits and last turns (`DEJA_INCLUDE_SUBAGENTS=1` whole, `=0` none) |
| Zed | `zed.go` | threads in the SQLite store at `Zed/threads/threads.db` |
| Devin CLI | `devin.go` | one SQLite store at `${XDG_DATA_HOME:-~/.local/share}/devin/cli/sessions.db`, the live chain walked back over `parent_node_id` and deduped by `message_id`, subagent runs read as side chains |
| Crush | `crush.go` | SQLite databases named by `projects.json`, plus `<project>/.crush/crush.db` |
| Cherry Studio | `cherrystudio.go` | Claude-format JSONL under the app's `Data/Agents/.claude/projects`, plus pi and dsh logs under `Data/Agents/.pi` and `.dsh`; a data dir moved in the app's settings is read from `~/.cherrystudio/boot-config.json` |
| Kilo Code | `kilo.go` | task JSON under the VS Code extension's storage, plus the CLI's `kilo.db` |
| Kiro | `kiro.go` | CLI JSONL under `~/.kiro/sessions/cli`, and `messages.jsonl` per IDE session |
| Command Code | `commandcode.go` | JSONL under `~/.commandcode/projects` |
| ZCode | `zcode.go` | JSONL under `~/.zcode/projects`, plus the CLI's `db.sqlite` |
| gajae-code | `gjc.go` | JSONL under `~/.gjc/agent/sessions` |
| Senpi | `senpi.go` | JSONL under `~/.senpi/agent/sessions` |
| Kimchi Coding | `senpi.go` | JSONL under the harness directory's `sessions`, one directory per encoded cwd |
| Hermes | `hermes.go`, `hermes_pg.go` | SQLite `state.db` under the Hermes home and per profile, plus Postgres when `DEJA_HERMES_PG_DSN` is set |
| deja notes | `notes.go` | `deja remember` entries in `notes.jsonl` |

File-based sources are parsed with a worker pool sized to `runtime.NumCPU()`. Results are collected by input file index and then appended in sorted path order, so parsing can be parallel while index writes stay deterministic.

Every SQLite store above is read through the local `sqlite3` command — opencode's and the schemas that borrow it, Cursor IDE state, Goose, Zed, Crush, Kiro, Devin, Hermes, and the databases Grok and OpenClaw keep beside their JSONL. Cursor CLI transcripts are plain JSONL; their tool results come from the chat's `store.db` beside them. There is no CGO SQLite dependency. Before trusting it, deja asks the `sqlite3` on PATH one JSON query per process: a wrapper that drops its arguments or a stub that prints nothing would otherwise read every store as empty, so a binary that does not answer is reported by path, as a missing one is.

Every one of those reads is given up once it stops moving: ten minutes by default with no byte from the sqlite3 child, where a byte is a row or a line `.progress` prints every thousand steps of SQLite's virtual machine, so a sort or scan with no row yet still counts as moving. sqlite3 buffers its output on a pipe, so those lines arrive a few hundred at a time, seconds apart on a slow disk. A sqlite3 without `.progress` is judged by its rows. One sqlite3 child once ran 13m54s with 0.75s of CPU in deja itself and nothing in the tree set a deadline; a fixed ten-minute wall clock that followed cut a Hermes store still reading on a slow disk. A store given up, or whose read fails, is an ordinary read error: the run names it, exits 3, `deja doctor` names it, and the rest of the index still builds. A rebuild that cannot read a store keeps what the last build held from it and reads it again on the next pass. `DEJA_STORE_TIMEOUT` takes a duration, and a zero or negative one turns the bound off for someone who would rather wait than lose a store.

A transcript read has the same budget, counted from the last byte read through the line and document readers rather than from the start, and so do the walk's reads of a changed transcript's head and tail. A file on a mount that stopped answering is skipped and named as unreadable once it has moved nothing for that long, and a slow read that keeps moving is left alone. A read given up marks its directory, so the rest of that directory is skipped at once for the rest of the process instead of costing the budget again. Its goroutine is not waited for; if the blocked call ever returns, what the parser reports after that is dropped. A sqlite3 child read inside such a watch moves it as its bytes arrive. `os.ReadFile` in a few parsers gets the budget as a plain wall-clock limit.

## Index format

Default path: `~/.cache/deja/index.db`.

Files:

- `records.bin`: length-prefixed records. Each record stores session key, source path, role, text, and timestamp. The session key, path and role are interned ids in the record's prefix, outside the body, so a scan for one kind of record reads the prefix and skips the rest; bodies of 8 KiB and up are deflated, smaller ones are not, because the read side pays more for a lower floor than the index saves.
- `buckets/*.bin`: token bucket files. A token maps to compact postings: record offset, session ordinal, and one bit marking the posting as a work record. A pass that adds records puts their postings in overlays beside the bucket (`tx.1.ov` next to `tx.bin`, same format) instead of writing the bucket again; readers join a bucket and its overlays. Overlays are merged pairwise while the newer is at least half the size of the one before it, and into the bucket once they hold a quarter of it.
- Dead records: a pass that replaces sessions (a transcript rewritten rather than appended to) appends their new records to `records.bin` and lists the offsets of the records they replace in the manifest. Every walk of the log and every postings read skips them. The pass stages its index in a new directory of hard links to the old files, so the swap is still the commit; each session it writes gets a fresh key id in the string table, so a reader holding the previous manifest resolves the appended tail to no session. When dead records reach a quarter of the log, the next replacing pass writes the log again without them, as it does where hard links are refused. A pass killed mid-append leaves `append.pending` with the old size, and the next lock holder cuts the log back to it. Once the swap commits, the pass zeroes the dead records where they stand: the frame keeps its length and the three ids all name the record's role, so any walk, an older deja's included, reads an empty record of no session. Before that it zeroes, in the directory of each postings file, every word only dead records held, past the letters the bucket is named after; the entry and its block stay, holding dead postings only. The log fingerprint in `recoffsets.bin` and `frictext.bin` moves to the zeroed log. A pass killed before the zeroing leaves `scrub.pending`, and the next lock holder finishes it. A swap that cannot remove `index.db.old` because a reader holds it (Windows) parks at `index.db.old.N`; recovery restores the highest.
- `recoffsets.bin`: where each session's records sit in `records.bin`, so reading one session back skips the walk over the whole log. It is a cache checked against the log: missing or stale, it costs the walk and nothing else. A replacing pass that appends in place reads through it only the sessions whose files changed or went; it walks the log when one of those files is a shared store or a prompt log, whose records name other paths.
- `commands-state.bin`: each session's part of the command table, so a pass that rewrites a few sessions mines only theirs for `commands.gob` and `sessionfacts.gob`. It names the log, its size, its count of dead records and the facts file written beside it; a state that does not match is walked again.
- `frictext.bin`: the first line of each repeated error per session, written at build time so the session-start block does not read those sessions back. `fixraw.gob` keeps every fix pair before judging, so an update judges them as a rebuild does.
- `manifest.gob` / `sessions.gob`: index version, source file state, redaction counters, sync export watermarks, imported-record dedupe keys, session metadata (including ordinals, the files a session touched most, hashes of the questions it asked, and the other transcripts that hold its id), build time, and search scope.

### Roles

Beyond `user`, `assistant` and `developer`, records carry what the agent did:

| role | holds |
| --- | --- |
| `tool-output` | what a tool printed. Claude files this under the user role in its own transcripts, which is why it used to arrive labelled as something a person said |
| `files` | the paths a turn opened or edited |
| `command` | a shell command worth keeping — an allowlist of build, test, VCS and deployment tooling, single-line only |
| `edit` | a span an edit replaced: the path on the first line, the exact removed bytes after it. Only the path earns postings, since nothing searches the body |
| `summary` | a harness's own digest of a conversation it compacted away. Kept, because it is the only record of the turns that went, and not filed as speech: on one store 1,906 of them were 19.6% of everything indexed in the sessions that had them, and none of them ever won a quoted line for a real question |

These are indexed and searchable by `--role`, and served in ordinary results only when asked for by role: a path that happens to contain the words of a question is not an answer to it. The postings carry a bit for them so the per-session read bound can spend its budget on speech first.

## Notes source

Explicit notes are stored as one JSON object per line in
`~/.local/share/deja/notes.jsonl`, or under `XDG_DATA_HOME`; `DEJA_NOTES_FILE`
overrides the path. Each record contains an RFC3339 `ts`, `project`, and
`text`. Notes are grouped into one user-message session per project and
calendar day in the local zone of whichever run indexed them, then redacted and
indexed like every other source. `deja index` regroups buckets minted in
another zone. The file is primary data; the index remains a rebuildable cache.

## Secret redaction

`internal/redact` runs before every `writeRecord` path: cold rebuild, `writeSessions`, non-append incremental replacement, and append-only incremental ingest. The pass is disabled only when `DEJA_NO_REDACT=1` is set; that escape hatch is unsafe because plaintext credentials will be written to the local index.

The redactor replaces only secret values, keeping keys and surrounding prose searchable. It covers AWS access keys and AWS secret assignments, generic credential assignments in ASCII and in other scripts, bearer tokens (and the `Authorization: Token` scheme) and JWTs, PEM and PGP private key blocks (whole, cut short, indented or JSON-escaped), Slack and Discord webhook URLs, provider token prefixes, connection URLs with `scheme://user:pass@host` credentials, credentials handed to a program on its command line (`sshpass -p`, `mysql -pSecret`, `curl -u user:pass`, `--password` and its siblings), netrc and cookie lines, bare high-entropy values in secret-shaped positions, and a password stated in prose — "the admin password is …", where no delimiter exists for the other rules to find. Text holding terminal escape sequences is read again without them, so colored output cannot split a value from its key.

Each rule that could match ordinary text carries a gate, and the gates are the part worth reading before adding a rule: the prose form requires the value to hold a digit or a symbol and to end at the first space, so "the password is wrong" and "the password is the same as staging" are left alone; the entropy rule excludes hex digests, UUIDs, paths and identifiers; the long command-line flags leave an ordinary word alone when the separator is a space rather than an `=`. `docs/SECURITY-MODEL.md` states the boundary these add up to.

Redaction counts are accumulated per source file in `FileState.Redactions` and as a manifest total. `deja sources` reads those counters and prints `redacted=` per harness.

Search flow:

1. Tokenize the query.
2. Read posting lists from the token buckets.
3. Intersect posting lists for multi-word searches.
4. Filter postings by session metadata and read the matching candidate records;
   past 1,000 matching sessions a page reads only those that can reach it (see
   [Ranking](#ranking)).
5. Group records back into sessions and score them in `internal/search` with BM25
   (`k1=1.2`, `b=0.75`). Document frequency and document length are measured
   over the candidate records at search time. User-message term contributions
   receive a 1.3 multiplier, and the score is multiplied by `0.5 + 0.5/(1+age_days)`,
   so age can halve a score but not erase it.
6. Sort by score, then updated time descending, then session ID ascending; the
   normal result limit is applied after ranking.

`--harness`, `--project`, and `--since` are applied from session metadata before
scoring. `--role` is applied while reading candidate records.

Regex search scans records because arbitrary regex cannot use token postings safely.

`deja blame` retrieves candidates using the basename stem through the existing token
postings, then verifies the basename as a path component or standalone word in the
candidate text. Full and longer suffix path mentions outrank bare basenames; mention
counts are blended with recency and an absolute project-root match receives a boost.

`deja blame <path>:<line>` answers about one line before that listing. Git names the
commit that last changed the line; the session is the one whose `edit` record
replaced the same text that commit shows as deleted, which is what makes the claim
"this session performed this change" rather than "this session was open nearby".
Spans are compared on whitespace-collapsed text and only from 24 runes up, since
`}` and `return nil` are in every diff. Where no session matches, or git cannot
answer, the line gets one sentence saying which silence it is — including the line
count when the line is past the end of the file. No reason is printed with it: over
81 attributed lines the session's own conclusion overlapped the change 0 times, so a
line lifted out of a session would read as the reason for a change it has nothing to
do with.

`--attribution` serves that answer alone, and with `--json` as one object naming the
rule that answered (`replaced` or `wrote`); `--git-note` writes it to
`refs/notes/deja`, idempotently and only where a session is named. Every shape deja
prints that names a file arrives with its recogniser in `internal/search`, since a
transcript keeps what deja printed and blame reads transcripts.

## Sync format

`deja sync export <dir>` reads `records.bin` and writes JSONL batch files named `deja-sync-<source-hash>-<timestamp>.jsonl`. Each line is one object:

```json
{"harness":"claude","session_id":"abc123","project":"api","role":"assistant","text":"fixed by ...","time":"2026-07-14T12:00:00Z","origin":"laptop"}
```

`origin` is the name of the machine the work happened on, and the current exporter writes it on every record: `DEJA_MACHINE` when set, else the first label of the host name. A session relayed with `--include-imported` keeps the name of the machine it came from rather than taking the relay's. It is absent only from batches written by older exporters and on a machine with no host name; the receiver then says only that the session came from elsewhere. It is the sender's own name for itself, not a verified identity, so the trust policy groups imported sessions by project rather than by it; `deja last --from <machine>` filters on it.

The export watermark is per peer and source path (falling back to session key for synthetic records) and is stored in `manifest.gob` as the max exported record timestamp. Re-running export emits only records with a newer timestamp for that source and that peer, so what one machine has already received says nothing about what another still needs. An export with no peer named keeps the bare source key, which is what manifests written before per-peer watermarks carry. Text is redacted again during export.

`deja sync import <dir>` reads all `*.jsonl` batches, runs each record's text through the redactor again under this machine's rules (counted in `stats --redaction` like local ingest), appends records to the local index, updates touched token buckets, and writes imported session metadata with the original harness and an `imported:` project prefix. The import holds a peer's records to this machine's switches: what `DEJA_INDEX_*` and `DEJA_INCLUDE_SUBAGENTS=0` leave out of a local read, a store `DEJA_EXCLUDE_HARNESSES` or a `harness:` line excludes, and a project the policy `ignore` rule covers stay out, and the import says how many. Imported IDs are namespaced (`imported-<hash>`) so they do not clobber local sessions from the same harness. The manifest stores dedupe keys of `harness:session_id:time:role:text-hash` (the older time-only key is still honoured), making re-import idempotent. Imported records live only in the index, so full rebuilds replay them from the old `records.bin` before regenerating from sources, and exports skip them to avoid echoing history back to its origin, unless `sync export --include-imported` asks to relay them.

### Which machines are one machine

The machines this one syncs with live in `peers.json` beside `policy.json`, outside the index, since who you sync with outlives any one rebuild. A bare `deja sync` walks that file and exchanges with every row, both ways.

A host is matched the way ssh matches it: case-insensitively, but only after the last `@`. `box` and `BOX` are one machine; `Root@box` and `root@box` are two logins on it. That rule decides three things at once — which row an exchange is recorded against, which rows a `deja sync forget` removes, and which watermark an export advances, so one machine cannot be sent its whole history twice under two spellings.

A run uses the spelling already stored for a host rather than the one typed, for the same reason.

The name a machine calls itself is its hostname, and an imported session is stamped with it rather than with the alias you type at it. `DEJA_MACHINE` replaces it, for a hostname you would rather not have on every record that leaves. A pull learns the pairing and records it on the peer's row, and `deja doctor` then counts what arrived from a machine under either name. A machine only ever pushed to has nothing to learn from, so it counts nothing — which is honest: nothing has arrived from it.

`deja sync ssh <host>` wraps the same export/import in one command: export to a temp dir, scp the batches, run the remote import (system ssh/scp, remote binary from PATH or `~/.local/bin/deja`).

`deja install sync-timer` runs a bare `deja sync` from a launchd agent or a systemd user timer. The unit carries the `DEJA_*` and `XDG_*` settings of the shell it was installed from, the set the hook launcher records, since neither service manager sees that shell; a timer run then syncs into the same index under the same switches.

Each of those phases names itself on stderr before it starts, repeats every ten seconds while it runs, and the run ends with where the time went; the remote's own output is echoed line by line as it arrives rather than after the ssh command exits, bounded and sanitised per line the way every other remote sentence is. Plain lines rather than a bar, because this output is read from a launchd log as often as from a terminal. Transfer says how many bytes in how many batches; nothing says anything per record or per path.

## Incremental algorithm

`currentFiles` records path, size, and mtime for known stores.

`EnsureForSearch` compares the current file set with `manifest.gob`:

- fresh manifest: do nothing;
- version or scope mismatch: rebuild. Two versions are tracked: the content version, which moves when deja derives something new from a transcript, and the on-disk format, which moves only when an older layout would be mis-read. A store whose content version is stale is re-read but keeps answering from what it has; only a format mismatch makes search say it cannot answer yet;
- append-only JSONL changes: append new records and update touched buckets. A harness whose sessions are re-read whole when they change (opencode, Kilo, ZCode, Cursor, Grok, Zed, Hermes, OpenClaw, Crush, goose) replaces those sessions instead (#4207, #4450);
- removed files or non-append changes: replace the changed sessions. Their new records are appended to `records.bin`, the old ones are marked dead and zeroed in place, and their postings go in overlays; the log is written out again without dead records once they are a quarter of it (see Dead records above).

A file takes the append path only when the prefix deja already read is still
byte-for-byte what it read: a rewind that truncates and regrows past the old
length looks exactly like growth, and appending onto it would leave the replaced
turns in the index for good. The recorded prefix hash is what tells the two
apart, so a live transcript that only grew is read from its last safe offset
rather than reparsed whole.

A file deja has never read has no prefix to check and no offset to resume from,
so it takes the append path whatever its kind: read whole, its sessions new, the
records already on file untouched. The gate used to demand a resume parser even
there, and because it applies to the whole batch, one new file of a kind without
one — a Copilot Chat transcript, an opencode session diff — sent every other
changed file down the rewrite branch too.

Search does not wait for the rewrite branch. `EnsureForSearchStale` runs the
cheap half inline and reports that the rest is outstanding; the caller refreshes
in a detached warmup and answers from the index it already has. Both the MCP
tools and the CLI take that path — a rewrite of a gigabyte of records and
buckets is not a cost a query can pay — while `deja index` and
`deja search --rebuild` still do the work in front of you.

`DEJA_NO_AUTO_INDEX=1`, or `"auto_index": false` in `policy.json`, turns all
of that off for a machine that indexes from cron. Every `Ensure*` then answers
from the index on disk or returns an error naming `deja index`, `requestWarmup`
spawns nothing, and the self-healing rebuild of a corrupt bucket becomes an
error. Only commands someone runs write: `deja index`,
`deja search --rebuild`, and the ones that change the index on purpose
(`forget`, `sync`).
Answers carry the index date in one line.

Cold rebuild does all parsing first, then writes `records.bin`, buckets, and manifest from one goroutine. That keeps the on-disk index coherent and avoids concurrent writers. The five sidecars derived afterwards — the co-occurrence map, fix pairs, the recurring-command table, the failures and the session facts — each write their own file and read nothing the others write, so they run together, and fix mining and the co-occurrence map also run per session across cores. That is most of the difference between a 51s and a 30s rebuild on a real store. A search-only index (`DEJA_SEARCH_ONLY`, `"search_only"` in `policy.json`) skips all five on every pass and records that in the manifest, so `deja fix` can say why it has nothing. Only a first build or `deja index` run by hand applies the setting, rebuilding once when it changes; automatic passes keep the one on disk, so a hook under another environment cannot rebuild the store back and forth.

## MCP server design

`cmd/deja/mcp.go` implements a small JSON-RPC stdio server.

Supported methods:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`
- `resources/list`, `resources/templates/list`, `resources/read` (`deja://session/…`)
- `prompts/list`, `prompts/get`

One tool, `deja`, with a required `mode`:

- `recall`: compact snippets for matching sessions.
- `context`: the markdown digest `deja ctx` prints.
- `blame`: the sessions that discussed a file, and what was decided.
- `fix`: what this machine ran after the same error last time.
- `how`: the real invocation for a tool here, from what agents ran.
- `orient`: the project's recurring commands and the files its sessions open, for an agent that would otherwise find them by reading.
- `remember`: stores one durable decision for later recall.
- `handoff`: the packet `deja handoff` prints, for an agent that reaches deja only through MCP.

Six tools were six envelopes declaring the same arguments, and six descriptions
each arguing they were the entry point — a question about a command went to
search instead of the capability that answers it (#1298). The old names still
answer a client that has them wired; `tools/list` does not carry them, so they
cost nothing per session.

The MCP server calls the same index/search code as the CLI. It writes protocol responses to stdout and keeps logs/progress off stdout so agents receive valid JSON-RPC.

## Interactive screen

A bare `deja` with a terminal on stdin and stdout opens the screen (`tuiWanted` in
`cmd/deja/tui.go`); `DEJA_TUI=0`, `TERM=dumb` or a platform without a raw mode gets
`deja brief` instead, and a pipe gets a few lines pointing at the commands. The module has no dependencies, so the terminal layer is its
own package:

- `internal/tui` sets raw mode (termios on unix, console modes on Windows), enters the
  alternate screen with mouse and bracketed paste on, decodes input into key, mouse,
  paste and resize events, and draws a cell `Canvas`. `Term.Draw` compares each row with
  the last frame and writes only the rows that changed, inside a synchronized update.
  Colour is truecolor, the 256 palette or none (`NO_COLOR`), from the environment. The
  reader returns every ~100 ms so `Close` can stop it before an agent takes the
  terminal; otherwise it would swallow that agent's first key.
- `cmd/deja/tui*.go` is the screen itself: list, reader, modals (continue in, agent
  filter, help, palette, what's new) and the first-run progress view.

`tuiApp.run` is one loop that owns all state. It renders, then waits on a terminal
event, a posted update, or a 100 ms tick. Slow work runs in goroutines and hands its
result back with `post(func())`, which the loop runs, so nothing outside the loop
touches state:

- the index refresh (`ensureForCLISearch`) after the first frame, which is drawn from
  the index as it was; on a first run `index.Ensure` instead, reporting into the
  progress view, with the newest sessions searchable before the build ends;
- search, 60 ms after a keystroke and skipped if a newer one arrived, through the same
  tier ladder and trust policy as `deja search` without the rerank and semantic tier.
  Filter words in the box (`codex:`, `today`/`yesterday`/`week`/`month`,
  `in:<project>`) are parsed out first (`tui_filters.go`) and become search options;
- after an empty answer, the same search across every project and then
  `index.DidYouMean`, which respells unknown words to the nearest word the index has;
- whole-session reads for the preview and reader, queued to one worker, which also
  builds the preview's story: the turns, the commands with how they ended, the files
  edited (`tui_story.go`);
- the Deleted list (sessions whose transcript is gone, which stats every file) and the
  "behind your uncommitted change" rows.

Stderr goes to `/dev/null` while the screen is up so index output cannot land inside a
frame. A resume or handoff picked on the screen is stored as a closure and runs after
`Close`, with the exact session picked and no index refresh.

## Claude SessionStart hook

`deja install --auto` installs the Claude MCP entry and adds a matcher-less command hook to `~/.claude/settings.json`:

```json
{"type":"command","command":"~/.config/deja/bin/deja-hook hook-context"}
```

The path in the entry is a launcher deja writes, not the binary it installed
from. The launcher is a short shell script that resolves the binary when the
hook runs — `DEJA_BIN`, the path the install ran from, `~/.local/bin` and
`~/bin`, the usual install locations, then the `PATH` — so a release, a Homebrew upgrade or an npm install
somewhere else moves the binary without any config being rewritten. On Windows
there is no launcher and the entry holds the binary's path, because a `.cmd`
cannot be exec'd the way a shebang script can.

`deja hook-context` is intentionally hidden from normal help. It first checks for a pending compaction recovery packet, which can exist before the first index build. For ordinary project recall, it derives the current project from the payload's `cwd`, else `CLAUDE_PROJECT_DIR` if the host exports one, else the directory it was run in, using the same Claude project-name logic as the parser, reads only an existing warm index (`manifest.gob`/`sessions.gob` must already exist), selects the most recent matching sessions by metadata project (ranked by the files the working tree is touching), leaving out a spawned agent's runs and a scheduled job's (`cron`) runs unless they are all the project has, leads them with the project's `accepted` promoted notes, and prints Claude's `SessionStart` response JSON with a compact markdown digest capped at 2KB (4KB in aggressive recall mode). It never builds the index on the session's path: a missing, outdated or damaged index starts `deja index` in a detached process and the hook returns without waiting for it. With no digest to give — the build still running, or no session from this project — it still answers rather than printing nothing: the machine-facts block when there is one, and otherwise, on the JSON path to a host that shows notes, a `systemMessage` saying what the build is doing (or that this machine has no agent history yet); `--plain` prints nothing in that case. Malformed input and other errors produce no output, and agent startup is never blocked. `--plain` prints the digest without the hook envelope, and `--once` gives it to the first turn of a session and nothing after — for a harness whose session-start output goes nowhere, where the digest has to ride the per-prompt hook instead (Kimi Code). `--notes` prints only the notes meant for the person (the one-time built note and the weekly count) and marks them shown; the dsh and OpenClaw plugins add it to their `/deja` reply, since neither host shows a person what a hook prints. Cursor shows a hook's message only when the hook blocks, so a payload carrying `cursor_version` gets no `systemMessage`, and neither does a session start under `--strict` or `--copilot`; the session-start receipt and the once-only notes are left unspent for the next host that shows them. A rewire after an upgrade still runs there and its note is dropped. The per-prompt "you have been here" window is kept per session, so the same gate on `hook-prompt` only keeps it from being stamped for a session that cannot show it.

`--auto` wires five more hooks with the same best-effort contract. The ordinary recall paths use a warm index; compaction capture reads the current transcript and stores a bounded packet without building the search index:

- **`hook-prompt`** (`UserPromptSubmit`) searches the index for the prompt's content and injects a small digest, or the `you have been here` line on a déjà-vu match. What it searches on is the ask — the last line ending in `?`, or the last line carrying a word, and within that line the last clause picked the same way — read before the rest of the prompt, so a pasted repo listing or stack trace above the question, or an instruction in front of it on the same line, does not spend the six-term budget. Nothing inside backticks or double quotes is cut, and command text (`hook-tool`, plan steps) is split by lines only, since `cd repo; go test` is one command. The same question from the same reader is answered once an hour: the per-session cooldowns count sessions shown, so an identical prompt on a timer used to walk one session further down the ranking on every tick. A spawned agent is exempt, because a fleet is many readers behind one id. What a spawned agent gets is the one-line pointer rather than the digest, unless its instructions repeat a question asked before: its block lands inside instructions the parent already wrote. The session asking is never quoted back to itself, and neither is the session that spawned it: a spawn's recall and an opencode task session's digest leave the parent out, since it is live and asking through the child. A scheduled job's run (Hermes `cron`) stays searchable but is not offered here: its prompt is the same every fire.
- **`hook-tool`** (`PreToolUse`, matched to the editing/command tools) reads the tool payload — a `Bash` command or an `Edit`/`Write`/`apply_patch` target — and injects one line naming that file's or command's prior decision. The command side is off unless `DEJA_COMMAND_HINTS=on`: measured over a month of sessions, a warned command failed no more often than any other, and a line that arrives with the command already chosen does not change it. Deliberately thin: it fires once per action, so it dedupes per agent session and carries at most one decision. A fact it has already given this session yields to the next one it can offer rather than ending the call — for a command that is the missing program, then the failure it hit here, held against the first program only when the rest of the line just filters its output — but only among those two, because the producers behind them rank and load sessions and cost a hundred milliseconds at the point of action. For a file it speaks from the second session that touched it, and only with something about that file: a promoted note or a decision that names the file or its directory, or a command those sessions ran and saw pass. A bare count, the newest session's closing sentence and a `deja blame` pointer are not said: over a month of real sessions none of those lines was acted on, and a project-wide note that does not name the file is already in the session-start block. A program this machine has never had is named from two sightings, and never while it is on the hook's PATH, and a promoted note says it is the project's standing decision rather than this file's. Where the sessions that touched the file also finished cleanly on a command, that command is what the line hands over instead of the conclusion — the one part of it the agent can check rather than believe, from `sessionfacts.gob`. `--plain` prints the block without the hook envelope, for hosts that take a string back from a handler rather than reading a hook's stdout; pi and omp use it, and they send a lowercase `read` because neither has a seam that runs before an edit. opencode is the same case for the same reason: its before-seam can only rewrite a tool's arguments, so the plugin sends the file action from `tool.execute.after` and folds the line into the tool's own output. dsh too: its plugin sends the file action from `tools/post-execute` and hands the line over as one of the result's `additionalContexts`. The same goes for the hosts that read a hook's context only after the tool: Gemini CLI's `AfterTool` on `read_file|replace|write_file`, Qwen Code's `PostToolUse` on `read_file|edit|write_file` and CodeBuddy's on `Read|Edit|Write|MultiEdit` (a `Read` counts there, and the reply names `PostToolUse`), the Cline plugin's `beforeTool`, the Hermes plugin's `transform_tool_result` on `read_file`, `write_file` and `patch`, the OpenClaw plugin's tool result middleware, the Amp plugin's `tool.result` on `Read`, `edit_file` and `create_file`, and prime-agent's `tool_result` for the files an `ipython` cell names. Grok 1.0.41 and ZCode hand `PreToolUse` context to the model, so Grok's matcher takes `Read` too and ZCode's takes `Bash|Edit|Write`.
- **`hook-tool-after`** (`PostToolUse`, matched to the command tools; also `PostToolUseFailure` in Claude Code and Qwen Code, which fire that instead for a command that exited non-zero, and the reply names whichever event it was sent; `postToolUseFailure` in Copilot CLI and Cursor, which fire it for a tool that errors rather than returns) answers a failed command with the pair already on file: the error seen before and what followed it without failing. Only pairs from the current project, or recorded with no project, are offered, except for a program or module this machine does not have: there the command that ran without it (the wrapper dropped, or another program doing its job on the same arguments) is offered from any project. A line the shell refused (`command not found`, `No module named`, zsh's `== not found`) is paired only with itself corrected, with such a substitute, or with an install, never with the next file the session edited. Never a remedy that cannot be taken back: a merge, a deleted branch, a force push, a hard reset, a dropped stash, `git clean`, a recursive `rm`, or a kubectl or helm change or anything against a prod namespace. Two lines at most, 420 bytes, deduped per agent session, and silent unless the store holds a pair for that error. What counts as a failure comes from the harness's exit status where it reports one, and otherwise from the recorded output, by the same rule `deja friction` uses. Copilot CLI gets it from `postToolUse` with `--copilot` (a non-zero exit is still a successful tool call there), and Hermes from the generated plugin's `transform_tool_result`, which appends it to a `terminal` result that did not exit 0. OpenClaw's `tool_result_persist` only rewrites the transcript, so the plugin uses the tool result middleware instead, which rewrites what the model reads after `exec`. TRAE CLI fires only `PostToolUseFailure` for a Bash that exits non-zero, so it is wired there too; ZCode fires `PostToolUse` with the exit code; prime-agent's exit code is only in the `BashResult` text of an `ipython` cell. Grok's `PostToolUseFailure` is for a tool that failed to dispatch or an MCP error, never a command's exit code, so the pair stays on its `PostToolUse`.
- **`hook-precompact`** (`PreCompact`) runs wherever a harness has a compaction event — Claude Code, Codex, TRAE CLI and IDE, CodeBuddy, WorkBuddy, Muse Code, Cursor CLI, opencode, Kilo CLI, Cline CLI, Qwen, Grok Build, Kimi, OpenClaw, pi, omp, Senpi, gajae-code, prime-agent, DeepSeek Harness, Copilot CLI and VS Code Copilot Chat, and Hermes through its memory provider; Reasonix's extension does the same in process on its own compaction event — and its first job is everywhere: forget what this session was shown, because compaction throws away the blocks while the list that stops them repeating outlives them. It then captures bounded structured state in the existing index manifest from the session the host names: Claude Code, Codex and CodeBuddy transcripts through a bounded tail reader; any other host's transcript file, or its session id in that harness's store (`--harness` names it when the payload does not, as Kimi's does not), through the source registry and the parser that indexes it; the compacted turns from Hermes' memory provider. TRAE IDE (an encrypted store) only forgets; [compaction recovery](compaction.md) lists which host captures and why the rest do not. Gemini CLI has no usable event for it, so the next `BeforeAgent` prompt hook finds the compaction in the transcript and captures it then. Antigravity, Crush, goose, Amp, Command Code, ZCode, CodeWhale and Junie have none at all and are caught up the same way: Antigravity's next `PreInvocation` from the `CHECKPOINT` step, Crush's next `PreToolUse` from `summary_message_id` in `crush.db`, goose's `Stop` from the agent-only compaction note in `sessions.db`, Amp's plugin on `agent.start` by comparing `messages()` with `messages({full: true})`, Command Code's next `PreToolUse` from the `compaction` record, ZCode's next prompt or tool hook from the `compact_summary` message in its CLI database, CodeWhale's next message or tool call from `artifacts/context-transfer-<id>.json`, and Junie's next prompt or tool call from `ContextCompactionBlockUpdatedEvent` in `events.jsonl`. Hermes without the memory provider is caught up by its plugin from the summary that appears in the history `pre_api_request` and `post_llm_call` carry. The next hook for the same session and workspace returns a recovery packet once, capped at 4 KiB including its trust frame. It records objectives, conclusions, verification commands, explicit gaps/conflicts, provenance, and repository freshness without requiring an agent checkpoint. Capture never waits for a full index rebuild. See [automatic compaction recovery](compaction.md) for limits, privacy controls, and the raw-actions-to-first-edit metric.
- **`hook-session-end`** (`SessionEnd`, Claude Code, Codex, Gemini CLI, Qwen Code, Kimi Code, Grok and goose; `sessionEnd` in Cursor CLI and Copilot CLI; `session.idle` and dispose in the opencode 1.x plugin, the end of a turn's execution and the setup's cleanup in 2.x; `session_shutdown` in the pi, Senpi, gjc, omp and prime extensions, and omp's `session_switch`; `session_end` in the OpenClaw plugin; `on_session_finalize` in the Hermes plugin; `afterRun` in the Cline plugin; `session/disposed` and process exit in the dsh plugin; `session.end` in the Reasonix sidecar; `agent.end` and dispose in the Amp plugin; `Stop` in the Antigravity plugin) drops the session's live stamp. Every hook stamps its session in `index.db.live`, and the MCP surfaces leave stamped sessions out for 20 minutes, so recall does not answer an agent with its own transcript. A sub-agent a stamped session spawned is left out with it: its hooks fire under the parent's id, so its own is never stamped. So is a fork's source, from Codex's `forked_from_id`, or for Claude Code and opencode, whose forks name no source, from the turn both open with: a fork copies its source's turns with their times, so the two share a first user turn to the millisecond, and the manifest keeps a fingerprint of it. Only a copy the asker already holds is left out, so a fork that went on is still there for its source. The hooks apply the same rule to the session they speak for, reading the first records of a transcript the index has not seen yet, and a Claude Code `SessionStart` with source `fork` gets no digest, since the fork carries the one its source opened with. When the harness says the session is over, it is history to the next one. Claude's entry carries no timeout: Claude Code gives a SessionEnd hook without one its own 1.5 s default, and holds `/exit` for the largest SessionEnd timeout it finds.
- **`hook-mcp-call`** (`preMcpToolCall`, Copilot CLI) restamps the session right before each MCP request, so a session past the 20-minute window is still left out of its own recall. It prints nothing: Copilot reads any reply as the request's new `_meta`.

## Ranking

Search intersects postings to find candidates, then scores each with BM25 over the query tokens and multiplies in a few bounded signals — each able to break a tie, none able to outrank plain relevance:

- **decision** — a session that reached a conclusion is lifted, and the decision-carrying line (not the query-match line) is what recall shows.
- **outcome** — a session whose own text reports it reverted an approach and settled nothing else is damped; one that reverted and then settled keeps the decision lift. A promoted `rejected`/`superseded`/`stale` lifecycle state travels with the session and is surfaced on every hit.
- **worn (reuse)** — a session agents keep recalling is lifted on a `log2` curve capped at +50%, on the theory that what the machine keeps needing is worth surfacing; the cap keeps popularity below relevance.
- **promoted note** — a curated note outranks the raw transcript it was distilled from.
- **freshness** — recency decays the score gently so time is a hint, not a filter.

A search that shows a page (`deja search` without `--all`, MCP `recall` and `recall_context`) does not read every session a common word matched. When more than `max(1000, 20 × page)` sessions match, it ranks them first from the postings and the manifest — matched records with an estimated occurrence count, estimated length, title, reuse, notes and age — and reads only that many, plus 200 others spread over the estimated lengths. Those 200 correct the length estimate the unread sessions enter BM25's corpus figures with, and the total counts the unread sessions too. Below the threshold, and with `--all`, every match is scored as before. The search screen reads the same way, 80 hits a page. Sessions the trust policy withholds on that surface are left out of the pool and counted as withheld, not as matches. The postings say which records hold every word, not whether a quoted phrase stands in them, so when any pooled session fails the text check, the unread and withheld ones are checked the same way and the unread ones that pass are ranked with the pool. Past 20,000 of their postings the counts are scaled by how many of the 200 passed instead, and the total says it is an estimate (`about N`, `total_estimated` in `--json`). MCP `recall` ranks the cwd's project first and the machine after from one search: the pool also reads the project's matches when they fit, and the project's answer is cut from them.

`recall_context` and the hooks reuse the same scored order; the point-of-action decision line comes from the same conclusion extraction the digest uses.

Reading one session back (`ctx`, `recall_context`, the session resource) puts the code it changed under the header, ahead of the conversation, so a digest cut to its budget loses the end of the talk rather than the fix. A cut says so where it happens: a turn cut short ends with how much of it was left out, the window ends with how many turns it showed and whether the last one is among them, and both name `deja show <id>`, which prints every stored turn; changes that did not fit are counted. The edits are read from the transcript at that moment (`sources.Changes`: Claude Code edits, Codex `apply_patch` calls, and for other harnesses any edit-shaped call in a JSON transcript that holds one session): the index keeps the written side of an edit only as line hashes, and that stays so.

## Semantic sidecar

`deja embed` writes `<index-dir>.vectors.bin`. The file begins with `DJV1`, a
version, vector dimension, model name, manifest generation, vector count, and
covered-record watermark. Each entry stores the records.bin byte offset, the
record's identity (a hash of its session, role, text length and the first and
last 256 bytes of text), its session key, and fixed-width float32 values. Writes use a
temporary file and rename. A rewrite of records.bin moves the offsets, so a
vector finds its record by identity once its offset no longer holds it, and
the next `deja embed` carries every vector to its record's new offset and
embeds only records it has not seen. The time stays out of the identity, since
harnesses that write no per-turn time date every turn from the file's mtime.
A changed model discards old entries.
Version 1 entries have no identity: they are carried only while the layout
they were made for holds, and search refuses them after a rewrite. Version 2
identities included the time; they are still matched and rewritten as version
3 on the next `deja embed`. A sidecar
that will not parse is treated as absent only by `deja embed`, which writes a
new one from scratch. Search and recall do not ignore it silently: they keep
the lexical order and print one notice saying semantic search is off until
`deja embed` runs again, the same way they treat a sidecar built for an earlier
index, and `deja doctor` reports it as unreadable.

### Hybrid reranking

Search first produces lexical BM25 results. When a matching sidecar exists, up
to 64 candidates are reranked using the query vector. The final score is
`0.5 * normalized lexical score + 0.5 * cosine similarity`. A failed query
embedding prints one notice and returns the original lexical order.

## Add a new harness

Implement the same shape as the existing sources.

Interface:

```go
func LoadNewHarness() []model.Session
func ParseNewHarnessFile(path string) ([]model.Session, error)
func ParseNewHarnessFileFromOffset(path string, offset int64) ([]model.Session, error) // if append-only
```

Six steps:

1. Add parser code in `internal/sources` that returns `model.Session` with stable `Harness`, `ID`, `Project`, `Path`, `Started`, `Updated`, and `Messages`.
2. Add a `Harness` entry to `allHarnesses()` in `internal/sources/registry.go`: `Load`, `Files`, and one `FileKind` per file shape with `Match`, `Parse` and, for append-only formats, `ParseFrom`. `Match` has to accept every root `Files` walks: the incremental index finds a changed file's parser by its path.
3. Discovery, path matching and incremental parsing in `internal/index` read from that entry; nothing there needs editing.
4. Add install/uninstall config handling in `cmd/deja/install.go` (and an `install_<harness>.go` when the wiring is more than an MCP entry) if the harness supports MCP.
5. Add an entry in `docs/registry/registry.json` and a `docs/registry/<id>.md` page linked from its README; the `registry*_test.go` files in `internal/sources` check them against the loader list and each other.
6. Add fixtures and tests for parsing, indexing, search, and install behavior.
