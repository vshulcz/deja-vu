// Package index builds and searches deja's on-disk index of past agent
// sessions.
package index

import (
	"errors"
)

// version 12 resharded non-ASCII tokens across buckets; 13 interned the key
// and source path of every record; 14 deflates the record payload; 15 drops
// the derivable offset from every bucket directory entry; 16 folds Traditional
// CJK to Simplified before keying, so an index written before it holds keys
// this build never queries; 17 filters out deja's own injected recall, which
// earlier builds indexed back in, so an existing index is rebuilt once to drop
// the copies it already holds.
// 18: file paths from tool calls are indexed (#558). An index built before this
// has none of them, so it is rebuilt rather than reused.
// 20: opencode tool output is parsed (#621) and each session records what it
// tripped over (#576). An index built before this has neither, and neither can
// be derived from what it does hold.
// 21: Codex sessions are keyed on the ThreadId rather than the SessionId
// (#635), so every existing Codex entry has the wrong identity, and the
// harness preamble Codex injects as a user turn is stripped (#636).
// 22: titles are derived by the rules from #671, #693 and #770. Those three
// shipped without a bump, so indexes built under the old rules kept the titles
// they derived then — incremental ingest reuses SessionMeta for a file it has
// already read, and no ordinary `deja index` re-derives it (#784).
// 23: the same shape again — #984 began recording an imported note's state on
// its manifest row without a bump, so a store that imported a batch before it
// kept a row with no state, and re-importing the batch adds nothing because it
// is deduped. The bump is what makes the one rebuild that re-derives it happen
// (#1049).
// 24: tokens() now folds NFD to NFC, so postings keys for accented words change
// shape. A store built before the fold keyed "café" decomposed; the rebuild
// re-derives every key on the composed form so an NFC query reaches it (#1098).
// 25: the fixes.gob and commands.gob sidecars are written only by a full
// rebuild, so a store built before they existed answered `deja fix` with a
// false "no session ran a command after that error" and the hook-tool command
// line with nothing — or, once the field was added, without the per-project
// session counts the trust policy needs. The bump forces the one rebuild that
// mines them both.
// 26: a session that opens with a greeting was titled "hi" — 17 rows of one
// real 800-session store, and the turn that says what the work was sits one
// line below (#790). Titles are part of this version for the reason 22 gives:
// incremental ingest reuses the row it already has, so nothing re-derives them
// without a bump.
// 27: the marks Japanese writes inside a word — ー above all, which appears in
// most loanwords — were not counted as CJK, so every such word was indexed as
// two runs and the bigrams came out of the pieces. A store built before this
// holds the split keys and a query built after it asks for the joined ones, so
// the bump is what makes the one rebuild that re-derives them happen (#1319).
// 28: what counts as a wall is bounded in characters rather than bytes, so a
// line written in Russian or Chinese is held to the same length as an English
// one. A store built before this holds fewer friction signatures, and nothing
// re-derives them without the bump — the same shape as 22 and 23 (#1319).
// 29: the prefixes a runner stamps on a line it did not write — pytest's `E `
// and a leading timestamp — are stripped before a line is hashed, so one error
// is one wall however it was printed. A store built before this holds both
// spellings under different signatures, and nothing re-derives them without
// the bump — the same shape as 27 and 28 (#1637).
// 30: a long digit run inside a line — a port, a pid, an epoch, a goroutine id
// — is masked before the line is hashed, so one failure is one wall across the
// numbers the machine hands out. A store built before this holds each run under
// its own signature, and nothing re-derives them without the bump — the same
// shape as 27, 28 and 29 (#2369).
// 31: what counts as a wall at all. A quote no longer drops a line as source
// (#2430), a generic opening no longer hides the phrase behind it (#2432), the
// list learned the walls a machine actually hits (#2434), a source line
// carrying an error is not one (#2436), and the line cap went from 120 to 200
// characters, which alone recovered a third again of what was recognised
// (#2438). Measured over a real store: 7,053 signatures before, 8,885 after. A
// store built under the old rules holds the old signatures and nothing
// re-derives them — `deja friction` reads the records and sees the difference,
// while `hook-tool-after` and the environment block read the manifest and stay
// silent, which is the state this bump exists to end (#2444).
// 32: goose sessions are kept unless goose says it wrote them for itself. The
// filter was an allow-list of `session_type = 'user'`, so the ways people
// actually reach goose — an editor over ACP, the CLI, a chat gateway, a run
// that kept no session — were dropped. A store built under the old rules is
// missing those sessions, and the db path only re-reads what has been touched
// since its watermark, so without the bump they stay missing until each is
// used again — the same shape as 21, 22 and 23 (#2873).
// 33: a combining mark continues the word it sits on. Latin NFD was composed
// away already, but Arabic harakat, Hebrew niqqud, Thai vowel signs and the
// Indic matras have no precomposed form, so the mark ended the token: كَتَبَ was
// indexed as three one-letter tokens and हिन्दी as two, and the words
// themselves were never in the index for any query to reach. A store built
// under the old rules holds the letters rather than the words, and nothing
// re-derives them without the bump (#1941). A variation selector is category
// Mn but says how to draw the rune in front of it, so it is not one of those
// marks and does not join the word (#2896).
// 34: Thai is indexed the way the other unspaced scripts are. Thai writes
// without spaces between words, so a sentence was one token cut at the byte
// cap and no query reached the words inside it; it now emits the overlapping
// bigrams CJK has had since 21. A store built before this holds the sentences
// and nothing re-derives the bigrams without the bump (#2897).
//
// The same bump carries one subtraction: a bigram of two function runes — 的了,
// 在哪, 什么 — is grammar, the query side has dropped it from the term list
// since it was written, and the index was storing the most frequent pairs in
// the language for nothing. Measured on a 42 MB Chinese corpus, buckets 75.5
// MB to 70.7 MB (#492).
//
// And one more subtraction on the same rebuild, because it is the same
// rebuild: a posting's session id is written as a delta against the previous
// posting in its block rather than in full. A block is sorted by offset and
// records.bin is written in session order, so on a real store 99.66% of
// postings already hold a session id no smaller than the one before them.
// Measured here at 5.91% of buckets on a mixed store, and by two contributors
// at 16.11% and 11.95% on theirs — none of those three is reproducible from
// this repository, which is why the first number is the one taken on the
// machine that merged it. The bucket magic moves with it so a reader that
// skips the version
// check — an index directory that cannot be locked is served without one —
// gets errCorruptIndex rather than session ids that are wrong without saying
// so (#492).
//
// 35 carries no format change at all — the parsers changed what they hand over,
// and an index built before them keeps rows the readers would no longer write
// (#3289). Kimi filed injections and its own hook's output under the person
// (#3199, #3235); Roo and Cline kept the <environment_details> listing inside
// every user turn (#3255, #3256) and yielded no tool results (#3269), no
// command or file records (#3295); aider kept /undo and /clear as questions
// (#3248) and its banner and continuation lines as speech (#3311); Hermes
// stamped every session with the profile instead of the directory it was
// worked in, and titled by whichever row came first (#3257, #3251, #3241);
// Zed threw away every tool result it stored (#3291), inlined its summary of a
// mentioned thread as the person's words (#3336) and gave every message one
// stamp (#3333); Claude Code's own records — skill bodies, re-fired prompts,
// the /fork notice — were indexed as the person's words (#3267); Qwen's tool
// results (#3281), Grok's tool input (#3285), Antigravity's edits (#3279) and
// plan approvals (#3326), Gemini's toolCalls (#3293), opencode's synthetic
// parts (#3299), subagent parents (#3301) and its own session titles (#3315),
// Copilot's injected skills (#3305) and exit codes (#3369), Cursor's per-turn
// stamps (#3349); Crush is a new reader whose stores nothing had walked. None
// of that reaches an existing store without a re-read, and the repo's
// precedent for a content-changing parser fix is exactly this: #2875 for
// goose, #1383 for the greeting rule, #2905 for the Thai bigrams.
//
// The same bump carries the compaction summary that titled a session: the
// preamble introducing it is stripped before anything reads the turn, so the
// summary read as the first thing a person said and 23 of the last 800 sessions
// on a real store were named by it (#3439). Titles are written at ingest, so
// those 23 keep their names until the re-read this bump already forces.
// 36 is about what happens when something goes wrong, in two halves.
//
// The first is what counts as a wall at all. A shell's position marker is
// itself the error signal, and timeouts, the harness's own tool errors and the
// rest of the Python traceback tails join the phrase list (#3445). A session's
// friction hashes are written into the manifest at ingest — the same shape as
// 31 — so `deja friction` reads the records and sees the difference while the
// hook at the failure and the environment block read the manifest and stay
// silent. The bump is what ends that.
//
// The second re-mines the fix pairs. The miner asked two things of a candidate — does
// the remedy name what the error named, did another session do the same thing —
// and both are about words, so it missed the commonest repair there is: the
// failing command, corrected. Counted over 2,953 failed commands, 16% are
// followed by the same program with most of the same words and no failure,
// against the 112 confirmed pairs the word rules mine from the same corpus.
// Pairs are a sidecar written at build time, so an existing store keeps the
// ones it has until the bump forces the re-read (#3445 carries the other half:
// the errors those pairs answer).
// 37 narrows what a second sighting proves. A red test is repaired by an edit,
// so a bare command that followed one is the session moving on, and the same
// routine runs the same way every day — which was enough to promote it. On a
// real store 11 of the 360 pairs served answered a named test failure with
// `gh pr merge 2532`, `git checkout -q -b work477` or `git status`. They are
// sightings again, and the pairs already on file only re-mine on this bump.
//
// The same bump stores the failing command beside the remedy that corrects it.
// 107 of those 360 answers share no word with the error they answer — they are
// not a command about the error, they are the command that caused it, working —
// and shown alone they read as an unrelated line to trust.
//
// Storing it showed the rule that mines them comparing the wrong thing. The
// commands carry the shell prompt a harness stored with them, so `$` was the
// program of both sides, the same-program test passed for any two lines and the
// navigation guard guarded nothing: 36 of 163 such answers were a different
// program or `cd` elsewhere. The wrapper the shell could not find is now
// transparent too, so `timeout 12 launchctl …` is repaired by `launchctl …`.
// 38 moves a record's role out of its compressed body. Scanning for one kind
// of record — every command in the store, which is what `deja how` asks —
// inflated all 280,000 records to read the 20,000 it wanted, and 53% of that
// command's time was flate. The role is interned beside the session key now, so
// the scan reads a prefix and skips the rest. Records are the store itself, so
// the layout change is what forces this bump.
//
// 39 is the DeepSeek Harness rename. dsh writes its logs as `session.v3.jsonl`
// now, deja matched only the older names, and #3508 taught both the discovery
// walk and the incremental kind to accept them. Without a bump that fix reaches
// nothing already on disk: the walk finds the new files, the manifest says the
// store is current, and a machine with thirteen sessions keeps answering from
// the four whose old-named logs are still there — which is what the report on
// that pull request described, with `--rebuild` as the only way out.
//
// 40 files a harness's compaction summary under its own role. opencode marks
// one on the message — `summary: true` — and deja read it as the agent talking:
// 1,906 of them across 23 sessions on a real store, 3.97 MB of the 20.24 MB
// indexed in those sessions, 19.6%, and 42% of the worst one. Over 60 real
// questions none of them ever won a quoted line, so what they cost is the read
// bound: a fifth of the text those sessions offer it is a machine's summary of
// speech rather than the speech. The bump is what re-files the ones already
// indexed; without it they stay assistant speech until something else forces a
// re-read (#3384).
//
// 41 applies the argument-credential patterns to text already indexed. Eight of
// twenty-four planted secret shapes were stored in the clear — a password given
// to a program as a flag, a .netrc line, a Cookie header — and redaction runs at
// ingest, so #3535 reaches nothing already on disk without a bump: the fix would
// hold for the next conversation and leave every earlier one quotable, including
// through `deja sync export`, which sends records to another machine. Every
// earlier redaction fix shipped without one, which is why this bump is worth its
// re-read.
//
// 42 masks a password handed to a program as a long flag at the length people
// actually choose. The key-value patterns take any value of sixteen characters
// or more, which is right where a short value is as likely to be a word and
// wrong after `--password`, where the flag says what the value is: measured
// through an index pass, `mysql --password=MyRootPass2026` and `app
// --password=Pass2026Short` reached `deja show` and `deja share` in the clear.
// Same reasoning as 41 — redaction runs at ingest, so without the bump the fix
// holds for the next conversation and leaves every earlier one quotable
// (#3572).
//
// 43 masks a secret whose value is not ASCII. Every key-value pattern ended in
// `[A-Za-z0-9/+=._-]{16,}`, so a password written in the alphabet its owner
// types in was stored in the clear whatever the key word: 41 widened the key
// words to those languages and left the value class where it was. Same reason
// as 41 and 42 — redaction runs at ingest (#3587), and the password assigned
// with an equals sign at any length (#3588).
//
// 44 masks a secret handed to `--passphrase`, `--secret`, `--token` or
// `--api-key`. 42 covered `--password` alone, so `gpg --passphrase …` and every
// CLI flag that names an account credential went through in the clear. Same
// reason as the three before it — redaction runs at ingest (#3596).
//
// 45 stores what a session settled and, per command, which session ran it last.
// Both are derived — the older answers stay correct, which is why the redaction
// floor does not move — and both are what the point-of-action hook had to go
// looking for at the moment of the action: a ranking pass plus a whole-session
// load, 133 ms against 21 ms, on the surface that fires on every action and
// almost never on a command its session has run before. A store built before
// this has neither, and nothing re-derives them without the bump — the same
// shape as 22, 23 and 32 (#3001, #3605).
//
// 46 masks a secret written `api-key "…"` or `api key "…"`, and one assigned
// with a colon where the value is too short for the key-value rule —
// `x-api-key: "s3cretvalue"`. The gate in front of the quoted rule admitted
// three of the four spellings its own pattern accepts, so which spelling a tool
// happened to print decided whether the value was stored in the clear. Same
// reason as 41 through 44: redaction runs at ingest (#3614).
//
// 47 stops masking a value whose label says it is public. The entropy pass
// takes the word before the separator as the label, so a WireGuard dump —
// `public key: <base64>`, one line per interface and one per peer — read as
// `key:` and every one of those lines became `[redacted:entropy]`. Measured on
// a 2,719-session store, that was the largest single class inside the entropy
// tier, and none of it is a credential. This one moves in the other direction
// from 41 through 46: a store built before it holds `[redacted:entropy]` where
// a published key was, and only re-reading the sources brings the text back

// 48 reads a Codex rollout Codex has compressed, and the sessions it has
// archived. Codex rewrites a rollout as `rollout-*.jsonl.zst` once it is seven
// days old and reads either name itself, so a matcher wanting `.jsonl` alone
// stopped seeing every session older than a week — silently, since a file that
// is not a candidate is not a skip either. `archived_sessions` is its second
// directory, same JSONL, never walked. A store built before this holds neither,
// and nothing re-reads a file the old rule never matched (#3640).
// 49 masks a password stated in prose. Every other rule wants a delimiter — a
// colon, an equals sign, a flag — and a person telling an agent a password
// writes none: "the admin password is hunter2-2026" went into the index
// verbatim, so `deja show` and `deja ctx` read it back in the clear. Redaction
// runs at ingest, so a store built before this keeps the text until it
// re-reads its sources (#3729).
// 50 records the written side of an edit, as hashes. The replaced side answers
// for 7.3% of committed lines because 71% of commits delete nothing at all, and
// a line a commit added has no replaced text to match — what a session wrote is
// the only evidence such a line was ever in a session. One 64-bit hash per
// written line of 24 runes or more, so no new user text enters the index. A
// store built before this has no written side until it re-reads its sources
// (#3773).
// 51 reads the edits Zed and Antigravity make. Zed keeps an edit on the tool
// result rather than the call — the call holds a path and a sentence of intent —
// so 783 spans across 29 of 30 sessions on this store were never read, and
// Antigravity had the replaced side but not the written one. Both sides come
// from a unified diff either way. A store built before this holds neither until
// it re-reads its sources (#595, #3773).
// 52 reads the written side of a Copilot Chat edit. Its store keeps each edit
// as a range plus the text that replaced it, so the new text is there in full
// and the old text is not there at all — 655 edit groups on this store, and
// nothing about any of them was indexed (#595). It also records the whole-file
// writes Copilot CLI, Cursor and Kimi dropped: each counted its replace tool as
// an edit and its write tool as a path only, so a file created wholesale left
// nothing to attribute from.
// 53 labels a provider key by its provider even when it arrives as the value
// of an assignment, which is how a key actually appears. The provider pass ran
// after the generic key-value rules, so `GITHUB_TOKEN=ghp_…` was masked as
// `credential` and deja could say a session had pasted something but not what.
// Measured over the 54,269 windows holding a provider prefix in one machine's
// transcripts: `credential` 171→49, openai-key 137→189, github-token 87→123.
// The same values were masked before and after; a store built before this
// keeps the old labels until it re-reads its sources (#536).
// 54 keeps what a Claude subagent changed. Its run was cut to the task and the
// answer, and the edit, written and file records went with the prose, so blame
// never named the subagent that changed a file: 1,668 edits on one machine,
// none indexed (#4163). Paths inside a Claude Code worktree were also dropped
// as the agent's own files (#4164). A finished subagent transcript is never
// re-read, so only a rebuild brings either in.
//
// 55: an interactive Codex session was owned by its history.jsonl line and
// filed under the project "history" (#4180); the row is decided when a session
// is written, so a rebuild.
//
// 56: a Claude Code session run in a directory with characters outside
// [A-Za-z0-9] took its project from the folder name, where those characters
// are blanked, and landed under the parent; it now reads the recorded cwd
// (#4175). Projects are set when a transcript is read, so a rebuild.
//
// 57: a Cursor CLI session gains its tool results from the chat store — the
// exit status on a command and the output it printed (#4187). A finished
// transcript is never re-read, so a rebuild.
//
// 58: a Cursor CLI session takes its project from the chat's meta.json cwd;
// the folder name blanks a dot, a space or a non-ASCII character, so those
// were filed under a split or empty name (#4193). A rebuild.
//
// 59: a failed Gemini CLI command carries its exit status (#4208); a
// finished chat file is not re-read, so a rebuild.
//
// 60: a resumed Gemini CLI session keeps the prompts deja's recall was
// prepended to; Gemini's own resume history leaves them out (#4214). The
// chat file is re-read whole only when it changes, so a rebuild.
//
// 60 also: Qwen Code — an `edit` or `write_file` call leaves files, wrote and
// edit records (#4254), a failed command carries its exit status (#4255), and
// a session in a non-ASCII directory takes its project from the recorded cwd
// (#4258). Gemini CLI reads through the same dialect, so its `write_file`
// leaves a wrote record too.
//
// 60 also: a Qwen Code or Gemini CLI shell result is indexed without the
// report around it, so `Error: (none)` no longer reads as a failure and a fix
// pair is stored under the error itself, not `Output: <error>` (#4256).
//
// 60 also: a Hermes session keeps its tool calls and results — commands,
// files, edits and tool output (#4242).
//
// 60 also: a goose edit or write call leaves an edit and a wrote record, not
// only the path (#4265).
//
// 60 also: a failed Kimi Code command carries its exit status (#4262).
//
// 60 also: an encoded project folder whose path has a "_", "." or space
// resolves back to its directory, so sessions named from the folder (Qwen
// Code and Claude Code without a cwd, Cursor CLI, pi, omp and the rest) get
// the real project: `my_org/app`, not `org/app` (#4402).
//
// 60 also: an index that let Gemini's resume stub take a session's row lost
// that session's records, and only a re-read brings them back (#4213); an
// opencode reply written during a pass is read only when its session is
// touched again (#4207). Hermes compaction copies count once (#4296).
//
// 60 also: a Kiro CLI session keeps its tool calls and results, which were
// dropped as not text (#4299).
//
// 60 also: a Cline CLI run_commands or read_files result, written as a list of
// per-command entries, is indexed as tool output (#4315).
//
// 60 also: a dsh session gains its command, files, edit and wrote records from
// its tool/call events (#4291).
//
// 60 also: an Amp thread keeps its tool calls and each turn's own time
// (#4356).
//
// 60 also: Antigravity commands and files come from the planner's tool_calls
// (#4358).
//
// 60 also: CodeWhale's 0.9.6 read, write and edit tools leave files and edits
// (#4360).
//
// 60 also: Command Code's v3 transcripts are read, turns and tool calls both
// (#4370).
//
// 60 also: a Continue session gains its tool calls — commands, files, edit
// spans, written lines and tool output from toolCallStates (#4373) — and a
// turn is never dated after its file's mtime (#4376).
//
// 60 also: a Crush edit, multiedit or write carries the replaced span and the
// written lines (#4377).
//
// 60 also: an aider session carries the files aider added and edited and the
// commands it ran, and an /ask question is kept once (#4324, #4325).
//
// 60 also: an aider session's id is its history path and start time rather
// than its ordinal in the file, so a new history at the path of a deleted one
// no longer takes the kept sessions' ids (#4332).
//
// 60 also: a Zed thread gains the files its agent created with write_file
// (#4339).
//
// 60 also: a Cherry Studio reply indexed mid-stream is read whole once it
// finishes (#4346).
//
// 60 also: an omp session indexed mid-way keeps its header id instead of
// splitting off the appended turns under the file name (#4406).
//
// 60 also: a CodeWhale edit_file call written with search/replace, its own
// argument names, leaves an edit and a wrote record (#4404).
//
// 60 also: a Kilo CLI or ZCode session doubled in the index on each pass after
// a write to its database (#4396); the copies already held go only on a
// rebuild.
//
// 60 also: a Kimchi sub-agent run is skipped rather than indexed as a session
// of its own (#4401); runs already held go only on a rebuild.
//
// 60 also: pi, omp, OpenClaw, gjc, prime, senpi and Kimchi sessions carry
// their tool calls as files, commands and edits (#4113); a finished
// transcript is not re-read, so a rebuild.
//
// 60 also: a Senpi eval cell's commands, reads and edits are indexed, and its
// result text is unwrapped (#4425).
//
// 60 also: a pi, Senpi, omp, OpenClaw, gjc, prime, Kimchi or Command Code session takes its
// project from the header's cwd as it is, not decoded from the folder (#4427).
//
// 60 also: a ZCode CLI session keeps its Bash, Read, Edit and Write calls
// (#4428).
//
// 60 also: ZCode's legacy snapshots under ~/.zcode/v2/sessions are read, and
// one rewritten is held once (#4432).
//
// 60 also: Roo and Kilo extension tasks read apply_patch, search_replace,
// edit_file and edit calls into files, edit and wrote records (#4419).
//
// 60 also: a Roo or Kilo extension turn is stamped at its own ts, not at the
// task's last activity plus N seconds (#4420).
//
// 60 also: the Roo and Cline "You did not use a tool" retry prompt is not
// indexed as a user turn (#4421).
//
// 60 also: Roo, Kilo and legacy Cline tasks from the XML tool era read their
// calls into command, files and edit records and their results as tool
// output (#4424).
//
// 60 also: a rooted DeepSeek TUI, Codex or pi-family tool path from the other
// OS's convention is no longer joined onto the session's cwd, a relative one
// under a slash-rooted cwd keeps slashes on Windows, and a Roo path with one
// leading `\` stays as written (#4438).
//
// 60 also: a line written while a pass ran is held once, where the pass read
// it and the next read it again (#4442); copies already held go on a rebuild.
//
// 60 also: a Codex, Copilot CLI, Kimi or pi-shaped command whose result came
// a pass after its call carries its exit, and a refused edit is dropped (#4443).
//
// 60 also: a Codex session known only from history.jsonl is one session per
// id, not per line, so a full build derives it from every prompt (#4449).
//
// 60 also: a grok, kiro-cli or Kimi reply streamed across an index pass is
// one message, not two (#4445).
//
// 60 also: a kiro-cli reply or tool call appended after a pass takes the
// prompt's time, not 0001-01-01 (#4444).
//
// 60 also: a Roo, Kilo, Cline VS Code, Reasonix or Kimi session whose title or
// workspace file changed alone is read again (#4446).
//
// 60 also: a ZCode snapshot restored into the CLI database leaves its turns
// to the database's on the next pass (#4448).
//
// 60 also: a Cursor chat continued or renamed after a pass is read whole, so
// its title, words, asked and touched match a rebuild (#4450, #4451).
//
// 60 also: codex, opencode, Kilo CLI, ZCode CLI, goose, crush, cursor, gemini,
// kimi, grok, antigravity, amp, aider, Copilot, Copilot Chat, zed, dsh, Cline,
// Roo, Kilo extension, Continue, CodeWhale, Kiro, Reasonix and Hermes name a
// project by the recorded cwd's last two segments, as claude does, and decode
// a file:// workspace first; claude and pi name a drive-root directory
// C:\proj "proj" rather than "C:/proj" (#4457, #4458, #4461, #4462).
//
// 60 also: an OpenClaw <id>.trajectory.jsonl is no longer read as a session
// (#4477).
//
// 60 also: a dsh log with a torn zstd frame keeps the frames around it
// (#4294), a Cherry Studio dsh log is filed under cherrystudio alone (#4342),
// a renamed Cline CLI session takes its new title (#4319), and a thin harness
// title is not retaken from an appended turn (#4452); rows already held
// change only on a rebuild.
//
// 60 also: a failed command carries its `→ exit N` in claude, the pi family,
// goose, cline, kiro-cli, zed and copilot-chat, and a copilot-chat terminal
// call its output (#4487, #4501, #4496, #4502, #4505, #4507, #4493).
//
// 60 also: a Claude Code PowerShell call is a command and a NotebookEdit call
// leaves files and wrote records (#4489).
//
// 60 also: a codex shell_command call is a command with its exit (#4490).
//
// 60 also: a Copilot CLI view call leaves a files record and an apply_patch
// call, whose arguments are the patch string, leaves files, edit and wrote
// records (#4491).
//
// 60 also: a Copilot Chat copilot_readFile call leaves a files record from
// its message uris (#4492).
//
// 60 also: a Gemini CLI read_many_files call leaves a files record for the
// literal paths in include (#4494).
//
// 60 also: an opencode or Kilo CLI 1.x edit or write call, and a 2.x write
// call, leaves edit and wrote records (#4495).
//
// 60 also: a Grok Build search_replace or write call leaves files, edit and
// wrote records (#4497).
//
// 60 also: a grok-dev session in grok.db keeps its tool calls and results as
// commands, files, edits, wrote and tool output (#4498).
//
// 60 also: an OpenClaw apply_patch call leaves files, edit and wrote records
// (#4500).
//
// 60 also: a Cline CLI editor call leaves a wrote record of new_text, and an
// apply_patch call files, edit and wrote records (#4503).
//
// 60 also: a Cline extension replace_in_file with Cline's own markers, or an
// apply_patch under input, leaves edit and wrote records (#4504).
//
// 60 also: a kiro-cli --v3 or Kiro IDE tool_call record becomes commands,
// files, edit and wrote records (#4506).
//
// 60 also: a Roo, Kilo Code, Continue, Amp or Antigravity command carries the
// `→ exit N` its result reports (#4530).
//
// 60 also: a Crush command carries its `→ exit N`, and an edit Crush refused
// leaves no edit or wrote records (#4532).
//
// 60 also: a failed ZCode Bash run carries its `→ exit N` (#4536).
//
// 60 also: a failed CodeWhale bash command carries its `→ exit N` (#4537).
//
// 60 also: a failed Command Code command carries its `→ exit N` (#4539).
//
// 60 also: a pi-family powershell call leaves a command record (#4523).
//
// 60 also: an omp or gjc edit in replace or patch mode leaves edit and wrote
// records (#4524).
//
// 60 also: an omp hashline edit leaves files, edit and wrote records (#4525).
//
// 60 also: a prime ipython cell's details.diffs leave files, edit and wrote
// records (#4526).
//
// 60 also: an Amp shell_command call is a command and an apply_patch call
// leaves files, edit and wrote records (#4527).
//
// 60 also: an Antigravity write_to_file call leaves a wrote record of its
// CodeContent once its step finishes (#4528).
//
// 60 also: a Continue edit_existing_file call leaves a wrote record of its
// changes, and a canceled edit leaves none (#4529).
//
// 60 also: a Roo or Kilo search_and_replace call with old_string and
// new_string leaves edit and wrote records (#4531).
//
// 60 also: a Roo or Kilo read_file call in the legacy files[] form leaves a
// files record (#4531).
//
// 60 also: a Crush lsp_replace_symbol call leaves a wrote record of its
// replacement (#4533).
//
// 60 also: a Kilo CLI background_process start or monitor is a command, and
// notebook_read and notebook_edit leave files and wrote records (#4534).
//
// 60 also: a Kilo Code task's search_and_replace operations[], fast_edit_file
// and write_file leave files, edit and wrote records, and delete_file and
// generate_image files records (#4535).
//
// 60 also: a CodeWhale terminal/run or task_shell_start call is a command
// (#4538).
//
// 60 also: a CodeWhale apply_patch call leaves files, edit and wrote records
// from its unified diff or replace[] entries (#4538).
//
// 60 also: a Command Code shell_command with args[], powershell or
// monitor_command call is a command, and a read_file glob is not a file
// (#4540).
//
// 60 also: a Reasonix notebook_edit, delete_range, delete_symbol or move_file
// call leaves files records, notebook_edit a wrote record and delete_range an
// edit record from its result's diff (#4541).
//
// 60 also: a Grok Build `grok -p` session, which grok marks "headless", is no
// longer stored with that as its spawn kind (#4585).
//
// 60 also: a Copilot Chat reply keeps the file and symbol names VS Code draws
// inline, from its inlineReference parts (#4589).
//
// 60 also: a Copilot Chat agent edit reads as the edited file's name in the
// reply, not as an empty code fence (#4590).
//
// 60 also: a Codex sub-agent's rollout records the thread that spawned it as
// its parent (#4547).
//
// 60 also: a Codex fork records the thread it was forked from, and every
// session keeps a fingerprint of the turn it opens with (#4549).
//
// 60 also: an OpenClaw 2026.7 reset or delete archive, stamped
// 2026-10-01T15-18-21.294Z rather than with a number, is read (#4482).
//
// 60 also: a Codex rollout grown by records with no message moves the
// session's updated time on an append, as a rebuild does (#4166).
//
// 60 also: a session whose id two files share takes its Started and Updated
// from both, whichever sorts first (#4253).
//
// 60 also: a Codex rollout compressed in place, or a transcript rewritten
// under its name in another form, is a move on an update with nothing else
// removed; its old path and records go (#4252).
//
// 60 also: removing one of two transcripts that share an id re-reads the
// other, so the runs both held stay and the row moves to it (#4310).
//
// 60 also: with DEJA_INCLUDE_SUBAGENTS=1, Kimi Code and Qwen Code sub-agent
// logs are read as sub-agent sessions of their parent (#4483).
//
// 60 also: a goose text_editor str_replace sent as a unified diff leaves edit
// and wrote records (#4287).
// 60 also: a session renamed to a thin title in the same pass as a new turn
// takes the title a rebuild gives it (#4592).
//
// 60 also: a row shared by two files keeps the span of the one that does not
// own it, so reading the owner again leaves Updated where a rebuild has it
// (#4574).
//
// 60 also: a Crush call the user denied leaves no command, files, edit or
// wrote record (#4575).
//
// 60 also: an omp patch-mode update that renames its file records the
// written lines under the new path and both paths as files (#4576).
//
// 60 also: an omp patch edit with no op is the update omp reads it as, and
// its lines are recorded (#4576).
//
// 60 also: a Kimi Code /btw side question is read from its fork, the
// question and the answer without main's copied context (#4484).
//
// 60 also: a DeepSeek Harness session.v4.jsonl is read, in place of the older
// log dsh leaves beside it, and a v4 error result keeps its edit out (#4600).
//
// 61 re-mines the fix pairs for a line the shell could not run. `== not
// found`, `command not found` and `No module named` were answered with the
// next file the session edited or the next thing it ran, and on one machine's
// repeat failures 110 of 119 answers had nothing to do with the error. Such a
// line now pairs only with itself in a form that ran, or with another program
// doing the missing one's job; otherwise nothing is said.
//
// 62 masks Telegram bot tokens. Redaction runs at ingest, the same reason as
// 41 through 46: the colon inside the token stopped every assignment rule, so
// a store built before this holds the ones pasted in prose or assigned to a
// variable as written.
//
// 63 files Claude Code's compaction summary and Gemini's state snapshot under
// the summary role, as opencode's and Hermes's already were (#3384). Read as
// the person's words, 41 Claude summaries supplied 191 of 622 first-hit quotes
// on 453 real recall questions.
//
// 64 masks the tail of a key-value secret that ends in punctuation:
// `--password=Sup3rS3cretValue!!xyz` was stored with `!!xyz` after the marker
// (#4682). Redaction runs at ingest, so only a rebuild drops the tails.
//
// 65: a CodeBuddy command carries the `→ exit N` its result text states, and
// the instruction prompt /compact writes as a user record is not indexed as
// the person's words (#4703, #4704).
//
// 66 drops the `Stdout: `/`Stderr: `/`Output: ` label CodeBuddy and Qwen Code
// put in front of a shell call's first line, so a fix learned there is keyed
// on the error itself (#4717). Only a rebuild re-derives the stored pairs.
//
// 67 files a session recorded in a WorkBuddy chat folder,
// <root>/<yyyy-mm-dd-hh-mm-ss>, under the root rather than one project per
// chat (#4735). Projects are set when a transcript is read, so a rebuild.
//
// 68 reads the compaction summaries Zed, Cline and Continue write, under the
// summary role, and Continue's no longer as the assistant's words (#4795).
// 68 drops the <turn_meta> block CodeWhale 0.10.0 saves in every user message,
// which was indexed as the person's words (#4802).
//
// 69 keys each fix pair on every error line of the output it answers, not only
// the first: `deja fix` with the line under a failing test's name found
// nothing, while friction counted it. Only a rebuild re-mines the pairs.
//
// 70: an incremental pass that rewrites the record log gives each carried
// record the postings a full build does. It tokenized whole texts and dropped
// the tool bit, so a long-lived index held twice the tokens of a rebuild and
// ranked tool output as speech; only a rebuild takes those postings out.
// 70: the append-only pass posts the same way. Appended records had no date
// keys and no tool bit, and an edit's replaced span was tokenized whole.
// 70: readers that dropped a store behind a byte order mark, stored a Copilot
// agent command twice or replayed a Command Code prompt on resume are read
// again, since only a rebuild takes the copies out.
// 70: redaction reads past terminal escapes and masks more shapes (key tails,
// cut-short keys, the Token scheme, webhook URLs); stored text holding them is
// only redacted again by a rebuild.
// 70: an append folds its tail into Settled, which it never updated, and the
// row records whether the session ran a command.
// 70: recoffsets.bin and frictext.bin sit beside the log. Both are caches
// checked against it, so an index without them reads as before and needs no
// rebuild for them.
// 71: Kimi Code 2.x sessions get their project and start time from
// state.json, which names the directory cwd and stamps in milliseconds; the
// rows already indexed have neither until a rebuild (#5143). Their first
// prompt no longer opens with the empty <hook_result> wrappers of deja's own
// hooks either, which is what titled them (#5144).
// 72: a Gemini sub-agent's first turn no longer opens with the
// <loaded_context> block Gemini puts ahead of its task; it titled every child
// session already indexed (#5214).
// 72: a Codex fork_context sub-agent keeps only its own turns, not the
// parent's history its rollout opens with, and a shell apply_patch records
// the edit it made; rows already indexed keep the parent's turns and no
// writer until a rebuild (#5210, #5211).
// 73: Qwen Code sub-agent logs are read by default, as Claude's are, so the
// files they wrote have an author (#5223).
// 73: an OpenClaw sub-agent hangs under the session that spawned it, a fork
// child keeps only its own turns, not the copy of its source's, and the
// internal-context block no longer opens a sub-agent's first turn; rows
// already indexed hold no parent, the copied turns and that title until a
// rebuild (#5219, #5220, #5221).
// 74: a Copilot CLI sub-agent run is its own session under its parent and a
// /fork names its source (#5216, #5217); rows already indexed hold neither
// until a rebuild.
// 75: a Grok --fork-session copy keeps only its own records, not the
// source's it was started from (#5235); forks already indexed keep the copies
// until a rebuild.
// 76: a goose sub-agent run is read under the session that spawned it, and a
// goose fork keeps only its own turns; both were indexed the old way
// (#5232, #5233).
// 77: a Kimi /fork keeps only its own turns under its source, a Kimi
// sub-agent keeps its task and is read by default, and a relative Write/Edit
// path sits under the session's cwd (#5226, #5227, #5228, #5229).
// 78: a Grok --fork-session copy reads as a fork of its source, and a Grok
// subagent hangs under the session whose subagent_spawned record names it
// (#5263, #5264); rows already indexed hold neither until a rebuild.
// 79: a Claude Code /branch is read as a fork of the session it names in
// forkedFrom, which the rows already indexed do not carry until a rebuild
// (#5241).
// 80: a dsh sub-agent names its parent, and a session's spawned family is
// left out of its recall at any depth; sub-agent rows already indexed hold no
// parent until a rebuild (#5279).
// 81: an opencode apply_patch with a relative header is filed under the
// session's directory, as the tool resolved it; rows already indexed carry the
// bare name until a rebuild (#5212).
// 82: a Cursor CLI subagent transcript is read by default as its task,
// answer and edits under the session that spawned it (#5243, #5244).
// 83: a Hermes delegate_task child is a sub-agent of its parent whatever
// source it ran under, files under its parent's project, and Hermes' relative
// file paths sit under the session's directory (#5248, #5249, #5250).
// 84: an OpenClaw heartbeat poll and its NO_REPLY are not turns, so a window
// that only got polls is no longer a session (#5319).
// 85: DEJA_INCLUDE_SUBAGENTS means the same in every reader: =0 leaves every
// sub-agent run out, unset cuts it to task, edits and last turns (#5301).
// 86: an OpenClaw JSONL sub-agent spawned before a /new stays under the window
// that spawned it, not the key's newer one (#5307).
// 87: an OpenClaw heartbeat poll answered HEARTBEAT_OK is not a turn (#5326).
// 88: an OpenClaw JSONL sub-agent whose spawning window was archived as .zst
// keeps it as its parent (#5340).
// 89: ё is filed as е, and queries fold it the same way, so «ещё» and «еще»
// are one word.
// 90: a row names every transcript that holds its id, and a pass that
// replaces the session reads them all (#5358).
// 91: a Claude Code prompt no longer carries the <pasted_content id="…">
// wrapper around pasted text, only the text. The tags titled sessions, gave
// the install's demo question the topic "pasted_content set" and were indexed
// as terms; the rebuild unwraps the sessions it carries from records.bin too
// (deleted, compacted away, imported), and a sync import unwraps on arrival.
// 92: a pass that replaces a few sessions appends their records and lists the
// old ones as dead, and postings land in per-bucket overlay files, so a pass
// writes what changed rather than the whole log and every bucket (#5387).
// 93: rows of a session several transcripts hold match a rebuild: a deleted
// transcript keeps the counts read while it was on disk and spans its own
// records beside copies, a copy that leaves takes its span with it, and the
// span of the files beside the owner no longer holds the owner's own (#5380).
// 94: a deleted transcript keeps every turn it held beside a copy: a copy
// that did not own the row is kept, a rebuild files shared turns under the
// deleted file, and a rewrite of the live copy leaves them with it. A
// renamed copy or one that moved to another id gives the row back to the
// smallest path, and a file holding only the last turn is not a move
// (#5441, #5442, #5443, #5444, #5445).
// 95: the command tables count what the record log holds: a turn two copies
// of a session share once, and of two transcripts sharing an id only the copy
// the row names, and a pass takes the words it removes out of the neighbour
// map (#5489, #5490, #5491).
// 96: a copy that takes a shared row from a transcript in another directory
// reads the old owner again, and copies read in one pass are written in path
// order, so a turn they share is filed under the copy a rebuild gives it
// (#5492).
// 97: fixes are mined from what the record log holds, as the command tables
// are: of two transcripts sharing an id only the copy the row names (#5504).
// 98: a rebuild writes a kept transcript and the copy on disk of the same
// session in the order the log held them, and takes a session's facts from
// its commands in time order (#5519).
// 99: a command whose clean result lands in the tail a pass appends is read
// with its result, so it carries the exit 0 a rebuild gives it (#5527).
// 100: a build's command tables go by the file each turn is filed under, a
// deleted copy's included; a copy changed beside a later one in another
// directory is read again with it when its tail holds a turn that copy has,
// and an append that moves a shared row mines its runs again (#5508, #5509,
// #5510).
// 101: a build drops the kept file of a deleted transcript whose sessions were
// all forgotten (#5521).
// 102: a pass that re-reads a changed transcript drops a record that strips to
// empty, as a rebuild does (#5555).
// 103: an incremental pass gives no row to a session it holds no record of
// (#5563).
// 104: a Copilot agent transcript's insert_edit_into_file records what it
// wrote (#595).
// 105: an apply_patch rename files its hunks under the new name (#595).
// 106: CodeWhale write_file and fim_edit, Crush lsp_replace_symbol and Reasonix
// delete_symbol record the side of the change they used to drop (#595).
// 107: Gemini resumed sessions drop repeated function responses (#5318).
// 108: a sub-agent run keeps its commands, and the outputs that failed or carry
// a runner's verdict, so its failures and fixes are mined (#5678).
// 109: a Claude Code row keeps the check and build rules its person typed,
// which recall marks as theirs, and a build writes receipts.bin, the records
// that back what a model said, which every later pass keeps up to date; rows
// and indexes built before have neither.
const version = 109

// onDiskFormat is how the store is laid out on disk — the record encoding, the
// bucket encoding, the manifest's own shape. It moves only when a reader of an
// older store would mis-read it, which is rarer than version: of the last four
// bumps, 35, 36 and 37 changed what deja derives from a transcript and left
// every byte's meaning alone, and only 38 moved a field.
//
// The distinction is what a reader needs during the rebuild a bump forces. A
// store whose content rules are stale still answers correctly under the old
// rules; a store whose layout this build cannot read answers nothing. Measured
// on a real machine: 8 of 56 recalls an agent actually made came back with
// "deja is rebuilding its index for this version — ask again then", and an
// agent does not ask again (#1733 says so about the refresh case). Three of the
// last four upgrades could have kept answering.
//
// 3: dead records and postings overlay files (#5387). A reader of format 2
// would serve the replaced records and miss the overlays.
const onDiskFormat = 3
const maxIndexedText = 64 * 1024

// maxRecordSize bounds a single serialized record. A record is one message
// (text capped at maxIndexedText) plus small metadata, so anything larger is
// a corrupt length prefix — reject it rather than allocate up to 4 GiB.
const maxRecordSize = 8 << 20

// bucketMagic moves whenever the meaning of the bytes behind it moves. The
// version bump above rebuilds the index on the next Ensure, but a directory
// that cannot be locked is served as it stands, with no version check at all
// (EnsureForSearch, EnsureForSearchNoWait) — the read-only container this
// repo deliberately supports. Reading old posting bytes under a new rule
// there would hand back session ids that are wrong rather than absent, and
// nothing would say so; failing the magic check instead makes it a corrupt
// index (#492).
//
// What that costs, stated because the first version of this comment said the
// opposite: on a writable index it is a cache miss — the recovery path
// rebuilds and the reader sees an answer. On a read-only directory it is not.
// The rebuild cannot run, so `deja search` exits non-zero with no results
// until the index is replaced by hand. That is the right trade against wrong
// session ids served silently, and it is a break for anyone shipping a
// pre-34 index inside a read-only image.
var bucketMagic = []byte("DJB2")

// errCorruptIndex marks unreadable index structures (e.g. a bucket file cut
// short by a crash). Callers treat it as a cache miss and rebuild.
var errCorruptIndex = errors.New("corrupt index")

// IsCorrupt reports whether err means the on-disk index is damaged and a
// rebuild will heal it.
func IsCorrupt(err error) bool { return errors.Is(err, errCorruptIndex) }

var lastIngestFiles int
