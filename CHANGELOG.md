# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]


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
- `deja install` with no target named none of the targets it knows, on the first command a new machine runs. (#830)
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

[Unreleased]: https://github.com/vshulcz/deja-vu/compare/v0.17.1...HEAD
[0.17.1]: https://github.com/vshulcz/deja-vu/compare/v0.17.0...v0.17.1
[0.17.0]: https://github.com/vshulcz/deja-vu/compare/v0.16.9...v0.17.0
[0.16.7]: https://github.com/vshulcz/deja-vu/compare/v0.16.6...v0.16.7
[0.16.1]: https://github.com/vshulcz/deja-vu/compare/v0.16.0...v0.16.1
[0.16.0]: https://github.com/vshulcz/deja-vu/compare/v0.15.7...v0.16.0
[0.5.0]: https://github.com/vshulcz/deja-vu/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/vshulcz/deja-vu/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/vshulcz/deja-vu/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/vshulcz/deja-vu/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/vshulcz/deja-vu/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vshulcz/deja-vu/releases/tag/v0.1.0
