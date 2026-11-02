package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const warmupRetryAfter = 10 * time.Minute

var spawnWarmup = startDetachedWarmup

type sessionStartHookResponse struct {
	// SystemMessage surfaces a one-line receipt in the user's UI when memory
	// actually landed; silent success builds no habit.
	SystemMessage      string `json:"systemMessage,omitempty"`
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// replyEventName picks the hook event a reply claims. Devin rejects a
// reply that names a different event than the one that fired, so a payload
// naming one of this hook's real events is honored — but only those. A host
// that puts its own spelling in hook_event_name (Cursor's camelCase
// postToolUse, Gemini's BeforeAgent) would otherwise have its reply renamed
// to something its own hook contract never produced.
func replyEventName(sent, fallback string, allowed ...string) string {
	if slices.Contains(allowed, sent) {
		return sent
	}
	return fallback
}

type precompactHookInput struct {
	SessionID      string   `json:"session_id"`
	ConversationID string   `json:"conversation_id"`
	TranscriptPath string   `json:"transcript_path"`
	CWD            string   `json:"cwd"`
	WorkspaceRoots []string `json:"workspace_roots"`
	HookEventName  string   `json:"hook_event_name"`
	Trigger        string   `json:"trigger"`
	// Harness names a host whose session has no transcript file: opencode and
	// Kilo keep it in SQLite, read here by session id; Hermes hands over the
	// turns it is compacting in Messages.
	Harness  string          `json:"harness"`
	Messages json.RawMessage `json:"messages"`
	// CatchUp is a compaction found after the fact, which may be handed over
	// again: it is captured once (catchUpCompactionWith).
	CatchUp bool `json:"deja_catch_up"`
	// Grok and Antigravity spell all of this in camelCase. See hook_grok.go.
	grokEnvelope
}

// adopt fills in what grok spells differently, so the session this compaction
// belongs to is the one deja forgets.
func (i *precompactHookInput) adopt() {
	i.SessionID, i.TranscriptPath, i.WorkspaceRoots = i.adoptCommon(i.SessionID, i.ConversationID, i.TranscriptPath, i.WorkspaceRoots)
}

// hookStdinWait bounds how long any hook waits for its payload.
const hookStdinWait = 300 * time.Millisecond

// readHookStdin reads the hook payload without trusting the host to close
// stdin. Codex keeps the pipe open and silent, and a hook that blocks on
// stdin hangs the whole session start — the harness then disables the hook
// and the user just sees memory quietly stop working.
func readHookStdin() []byte {
	return readHookPayload(os.Stdin, hookStdinWait) // os.Stdin read here: tests swap the global
}

// readHookPayload reads at most 1MB from r and gives up after wait, keeping
// whatever arrived by then. Waiting for EOF is waiting for the host: a payload
// can be complete on the wire while the pipe stays open behind it (#846).
//
// It returns as soon as the bytes so far hold a whole JSON value. Waiting out
// the deadline on a payload that already arrived would put those milliseconds
// on every user message, which is the cost the decoder this replaced did not
// have (#846).
func readHookPayload(r io.Reader, wait time.Duration) []byte {
	return readBounded(r, wait, true)
}

// readBounded is readHookPayload with a say in when to stop. stopAtValue=false
// keeps reading to EOF or the deadline: the status line drains what the host
// wrote rather than leaving the rest of it in the pipe, and only the waiting
// needed a bound (#1074).
func readBounded(r io.Reader, wait time.Duration, stopAtValue bool) []byte {
	var mu sync.Mutex
	var buf []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		lr := io.LimitReader(r, 1<<20)
		chunk := make([]byte, 32<<10)
		for {
			n, err := lr.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf = append(buf, chunk[:n]...)
				// A host on Windows can write its payload with a UTF-8 byte
				// order mark, which no JSON decoder takes, and the hook went
				// quiet on it.
				if len(buf) >= len(utf8BOM) {
					buf = bytes.TrimPrefix(buf, utf8BOM)
				}
				whole := stopAtValue && endsAValue(buf) && json.Valid(buf)
				mu.Unlock()
				if whole {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(wait):
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]byte(nil), buf...)
}

// endsAValue keeps the scan above off the hot path: json.Valid on every chunk
// of a large payload is a full pass each time, and a hook payload is an object
// or an array.
func endsAValue(b []byte) bool {
	b = bytes.TrimRight(b, " \t\r\n")
	if len(b) == 0 {
		return false
	}
	return b[len(b)-1] == '}' || b[len(b)-1] == ']'
}

// runHookPrecompact is deliberately best effort: Claude must be able to
// compact even when the input is incomplete or the index cannot start.
func runHookPrecompact(dir string) { runHookPrecompactFor(dir, "") }

func cmdHookPrecompact(dir string, rest []string) error {
	runHookPrecompactFor(dir, precompactHarnessArg(rest))
	return nil
}

// runHookPrecompactFor takes the harness from the command line when the
// payload does not name one: Kimi's PreCompact carries only the session id, and
// the hook entry is the one place that knows whose session it is.
func runHookPrecompactFor(dir, harness string) {
	var input precompactHookInput
	_ = json.Unmarshal(readHookStdin(), &input)
	input.adopt()
	if input.Harness == "" {
		input.Harness = harness
	}
	// A host that finds a compaction after the fact hands over the turns it
	// had before it and may hand the same one over again: captured once, like
	// Gemini's (Amp's plugin, on the next agent.start).
	if input.CatchUp {
		catchUpCompactionWith(dir, input, func() (sources.CompactionTranscript, bool, error) {
			// The file is the harness's format wherever it was written.
			t, err := sources.ReadCompactionSession(input.Harness, input.SessionID, input.TranscriptPath,
				compactionWorkspace(hookProjectPath(input.CWD, input.WorkspaceRoots)))
			return t, err == nil, err
		})
		return
	}
	// Compaction throws away the blocks this session was shown, and the list
	// that stops them repeating outlives them — so the memory the agent just
	// lost is exactly the memory recall refuses to send again. Forget what this
	// session was shown; everything else in the file belongs to other sessions.
	forgetInjected(dir, input.SessionID)
	if input.SessionID != "" {
		forgetInjected(dir, compactionFailureKey(input.SessionID))
	}
	captureCompaction(dir, input)
	requestWarmup(dir)
}

// precompactHarnessArg is the value of --harness, or "". A hook never fails
// over its arguments, so anything else is ignored.
func precompactHarnessArg(args []string) string {
	for i, a := range args {
		switch {
		case (a == "--harness" || a == "-harness") && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--harness="):
			return strings.TrimPrefix(a, "--harness=")
		}
	}
	return ""
}

// withoutSubagentRuns drops the sessions a parent spawned, and a scheduled
// job's runs (#4948). The caller keeps the original pool when this leaves
// nothing, so a project whose only history is such runs still gets a digest.
func withoutSubagentRuns(ss []model.Session) []model.Session {
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		// "sidechain" is Claude's word for the edge; Gemini, Codex, Cursor and
		// the rest say "subagent", Grok "subagent_fork" too (#5215). The same
		// words recall leaves out of a session's own lineage.
		if s.IsSpawned() || s.Kind == "cron" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// threadHasDigest reports whether a session starting now already carries a
// start digest. Codex keeps the context a hook added in the thread and replays
// it on resume, so every `codex resume` put the same digest in front of the
// model once more, and a fork, which copies its source's history, one more
// again (#4550). For Codex that is read off what deja told the thread, or the
// thread it was forked from: a thread whose ledger rows have rotated out gets
// one more, which is the old behaviour.
//
// Claude Code did not replay it on 2.1.287 and does from 2.1.300 on: the answer
// is stored as an attachment in the transcript and resent on resume (#5122).
// There the transcript says, so an older client that never stored one, or a
// session compacted since, still gets its digest. Every Claude-format host
// reads as "claude" here; one that stored the record without replaying it
// would lose its resume digest, and none is known to.
func threadHasDigest(dir, source, sessionID, transcript string) bool {
	if transcript == "" || sessionID == "" || source == "compact" || source == "clear" {
		return false
	}
	// Grok Build keeps the tool-hook reminder the digest went out in and sends
	// it again on resume, and a --fork-session copy starts with its source's:
	// every `grok -r` added one more digest to the request, and a fork got a
	// third led by the session it was forked from. chat_history.jsonl is what
	// the model is sent, so it says.
	if sources.IsGrokTranscript(transcript) {
		return sources.GrokChatHolds(transcript, sessionStartMarker) || sources.GrokChatHolds(transcript, sessionStartRuleMarker)
	}
	head, err := sources.TranscriptHead(transcript, sessionID)
	if err != nil {
		return false
	}
	if head.Harness == "claude" {
		return source == "resume" && claudeTranscriptHoldsDigest(transcript)
	}
	if head.Harness != "codex" {
		return false
	}
	told := func(id string) bool { return len(alreadyInjected(dir, sessionStartKeyPrefix+id)) > 0 }
	if source == "resume" && told(sessionID) {
		return true
	}
	return head.Kind == "fork" && head.Parent != "" && told(head.Parent)
}

// claudeTranscriptHoldsDigest reports whether a Claude Code transcript holds a
// deja start digest the client will send again: a SessionStart attachment
// carrying a recall block, after the last compaction boundary.
func claudeTranscriptHoldsDigest(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64<<10)
	held := false
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A record's type and hook name sit at its front, so the first
			// 64 KiB of a long line is enough to tell; the rest is skipped.
			line = append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
		}
		switch {
		case bytes.Contains(line, []byte(`"compact_boundary"`)):
			held = false
		case bytes.Contains(line, []byte(`"hook_additional_context"`)) &&
			bytes.Contains(line, []byte(`"hookEvent":"SessionStart"`)) && bytes.Contains(line, []byte(`deja-recall`)):
			held = true
		}
		if err != nil {
			return held
		}
	}
}

// sessionHadDigest reports whether this session has already had its one
// session-start attempt, for a host that fires that event every turn. Antigravity
// keeps the same record under a key of its own; this one is for hosts that come
// in through the ordinary session-start hook.
func sessionHadDigest(dir, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	return alreadyInjected(dir, onceDigestKey(sessionID))[onceDigestMarker]
}

func rememberSessionDigest(dir, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	rememberInjectedIDs(dir, onceDigestKey(sessionID), onceDigestMarker)
}

func onceDigestKey(sessionID string) string { return "once:" + sessionID }

// onceDigestMarker is what the ledger row says: this session has had its
// session-start attempt.
const onceDigestMarker = "once-digest"

// joinNotes puts a maintenance line ahead of the memory line without letting
// an empty one leave a stray separator.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

// noteWidth is the most a line meant for the person may take, in terminal
// columns. The hosts that show these lines are terminals and toasts; at 100 a
// line still fits a 120-column terminal after the host's own prefix.
const noteWidth = 100

// fitNote appends each optional part to base while the line stays within
// noteWidth, so a long tally drops out of the line instead of wrapping it.
func fitNote(base string, parts ...string) string {
	n := termwidth.Columns(base)
	var baseSb363 strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if m := termwidth.Columns(p); n+m <= noteWidth {
			baseSb363.WriteString(p)
			n += m
		}
	}
	base += baseSb363.String()
	return base
}

// receiptLine is the session-start receipt: the claim always, then the most
// re-used session by name (#579), then today's tally, each while the line
// fits. The name goes first because it is the part only this line says; the
// tally is also on the statusline and in `deja stats`.
func receiptLine(base string, reused reusedMemory, svc string) string {
	line := base
	if reused.Title != "" && reused.Times > 0 {
		tail := fmt.Sprintf(" %d×", reused.Times)
		room := noteWidth - termwidth.Columns(line+" · most re-used: "+tail)
		// Under a dozen columns the title is an ellipsis and two letters,
		// which names nothing.
		if room >= 12 {
			line += " · most re-used: " + quoteWithin(trimBriefTitleTo(reused.Title, 1<<20), min(room, 34)) + tail
		}
	}
	return fitNote(line, svc)
}

// quoteWithin quotes s the way %q does, cut with an ellipsis so the quoted
// form, escapes and quotes included, takes at most width columns.
func quoteWithin(s string, width int) string {
	q := strconv.Quote(s)
	if termwidth.Columns(q) <= width {
		return q
	}
	for n := termwidth.Columns(s) - 1; n > 0; n-- {
		q = strconv.Quote(strings.TrimSpace(termwidth.Cut(s, n)) + "…")
		if termwidth.Columns(q) <= width {
			return q
		}
	}
	return strconv.Quote("…")
}

// fitList joins names while they fit in width columns and counts the rest, so
// the receipt names what touched the session without running off the line.
func fitList(names []string, width int) string {
	out := ""
	for i, name := range names {
		next := name
		if i > 0 {
			next = out + ", " + name
		}
		if i > 0 && termwidth.Columns(next) > width {
			return fmt.Sprintf("%s +%d", out, len(names)-i)
		}
		out = next
	}
	return out
}

// indexDirWritable reports whether a rebuild could write where the index
// lives. Ensure builds into a sibling directory and renames it over the old
// one, so the parent is what has to be writable — not the index directory
// itself, which is why a store can rebuild fine while its own directory is
// read-only.
func indexDirWritable(dir string) bool {
	// The directory the rebuild actually writes into. Probing the parent alone
	// read a read-only index directory inside a writable parent as writable —
	// a cache directory owned by another user, a read-only subtree — and the
	// session start went silent again in the one state that never repairs
	// itself (#2499). The parent is still the right probe when the index
	// directory does not exist yet, since deja creates it.
	// Both, when both exist: a build writes its files inside the index
	// directory and its lock and temporaries beside it, so either one refusing
	// is enough to stop it.
	if !dirWritable(filepath.Dir(dir)) {
		return false
	}
	if dirExists(dir) {
		return dirWritable(dir)
	}
	return true
}

// stuckWiringNote names what the repair could not write. Without it the reader
// sees the same "deja rewrote its wiring" line on every session start — the
// repair really is retried each time, because the record stays unstamped until
// every target takes the new path (#2594).
func stuckWiringNote(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	return fitWarning(
		fmt.Sprintf("deja could not rewire %s — `deja install %s` says why", strings.Join(targets, ", "), targets[0]),
		fmt.Sprintf("deja could not rewire %s — `deja install %s` says why", fitList(targets, 40), targets[0]))
}

// rewireNote is the one line a session start spends on maintenance: which
// targets were rewritten when the binary moved or upgraded.
func rewireNote(targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	// States what happened rather than guessing why: on a routine upgrade
	// nothing was hand-edited, and claiming an edit was destroyed reads as an
	// accusation. Someone who did edit those commands still learns that they
	// were rewritten, and where to put the change instead.
	return fitWarning(
		fmt.Sprintf("deja rewired %s after an upgrade — `deja install` owns those commands", strings.Join(targets, ", ")),
		fmt.Sprintf("deja rewired %s — `deja install` owns those commands", fitList(targets, 48)))
}

// fitWarning returns the first form that fits noteWidth, or else the last,
// shortest one. A warning keeps its fact and its command whole, so when a long
// path runs even the short form past the width the line is left long: a path
// or a command cut in the middle is not something a person can act on.
func fitWarning(forms ...string) string {
	for _, f := range forms {
		if termwidth.Columns(f) <= noteWidth {
			return f
		}
	}
	return forms[len(forms)-1]
}

// unmountedIndexNote is the session-start line for an index whose directory
// and parent are both gone: a disk that went away, not a permission problem.
func unmountedIndexNote(parent string) string {
	return fitWarning(
		fmt.Sprintf("deja cannot find the index (%s) — disk unmounted? reconnect it or set DEJA_INDEX_DIR", parent),
		fmt.Sprintf("deja cannot find %s — disk unmounted? set DEJA_INDEX_DIR", parent))
}

// unwritableIndexNote is the line for an index that needs a rebuild in a
// directory deja cannot write.
func unwritableIndexNote(path string) string {
	return fitWarning(
		fmt.Sprintf("deja cannot rebuild the index: %s is not writable — `deja index` says why", path),
		fmt.Sprintf("deja: %s is not writable — `deja index` says why", path))
}

// indexCannotAnswerNote is the line for a read-only index directory inside a
// writable parent, which `deja index` rebuilds.
const indexCannotAnswerNote = "deja: the index cannot answer — recall is quiet until `deja index` rebuilds it"

// indexingNote is the line for a build that was asked for and has published
// no progress yet.
const indexingNote = "deja is indexing your history — recall starts when it finishes"

// recallIsOff reports the documented kill switch. One reader rather than a
// string compare per call site: the second site is what let the environment
// block past it (#2699).
func recallIsOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("DEJA_RECALL")), search.RecallOff)
}

// buildNotice is what a session start says while a build runs: the published
// progress if there is any, the bare promise if the build was only just asked
// for, and the reason instead when the index cannot be written at all. It is
// shared with the environment-facts path, which returned before ever reaching
// it — so a machine that had environment facts heard nothing at all through an
// entire post-upgrade rebuild, while the statusline showed the progress bar
// (#927).
func buildNotice(dir string) string {
	// Nothing builds on its own with automatic indexing off, so no promise of
	// one: what is missing and the command that builds it (#5385).
	if index.Manual() {
		return manualIndexNote(dir)
	}
	if st := readWarmupStatus(dir); st != nil {
		// On a machine no agent has written to, "indexing your history —
		// recall comes online when it finishes" is deja's first word to an
		// agent and none of it is so: there is nothing to read, and recall
		// starts when some agent writes a transcript, not in a few seconds
		// (#2407). Same claim the CLI makes about the same machine. A build
		// that has counted work to do is believed over the store walk: it is
		// reading something, whatever a later look at the stores finds.
		if st.Total <= 0 && noAgentHistoryFound() {
			return "deja: no agent history on this machine yet — `deja sources` shows where deja looked"
		}
		return st.line()
	}
	// A rebuild is pending either because one was asked for or because the
	// index on disk cannot answer as it stands. The second half matters
	// because the sentinel requestWarmup writes lives inside the index
	// directory: on a read-only one it is never created, warmupJustRequested
	// stays false, and this line — written for exactly that state — was
	// reachable only when the directory was writable. Every session went out
	// silent, in the one state that never repairs itself (#1048).
	if (warmupJustRequested(dir) || indexNeedsRebuild(dir)) && !indexDirWritable(dir) {
		// A disk that went away is not a permission problem, and telling
		// someone to check the permissions of a directory that is not there
		// sends them nowhere. `deja index` and doctor already separate the
		// two; session start is where the state is first noticed (#1054).
		if parent := filepath.Dir(dir); !dirExists(dir) && !dirExists(parent) {
			return unmountedIndexNote(parent)
		}
		// Which of the two states this is decides the sentence. A rebuild
		// writes the new index beside the old directory and replaces it, so a
		// read-only index directory inside a writable parent is rebuilt by
		// `deja index` — measured: permissions come back and the search
		// answers. What the hook cannot do there is start that rebuild itself,
		// since the sentinel requestWarmup writes lives inside the read-only
		// directory. Blaming permissions sent the reader to look at something
		// that is not the problem (#2502).
		if dirWritable(filepath.Dir(dir)) {
			return indexCannotAnswerNote
		}
		return unwritableIndexNote(unwritableIndexDir(dir))
	}
	if !warmupJustRequested(dir) {
		return ""
	}
	return indexingNote
}

// unwritableIndexDir names the directory to fix: the index directory when that
// is what cannot be written, its parent when the index directory does not
// exist yet and the parent is what refuses to hold it.
func unwritableIndexDir(dir string) string {
	if dirExists(dir) {
		return dir
	}
	return filepath.Dir(dir)
}

// indexNeedsRebuild reports that the index on disk cannot answer as it stands:
// absent, written by another format, or damaged. It is the condition every
// caller of requestWarmup already tested separately.
func indexNeedsRebuild(dir string) bool {
	return !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir)
}

// runHookContext prints session-start context. plain=false emits the Claude
// Code / Codex hook JSON envelope; plain=true prints the bare digest for
// hosts that inject raw text (the opencode plugin).
func runHookContext(dir string, plain bool) error {
	return runHookContextMode(dir, plain, false)
}

// once forces the one-digest-per-session rule on from the command line, for a
// harness that has no session-start channel and has to carry the digest on its
// per-prompt hook.
func runHookContextMode(dir string, plain, once bool) error {
	// A session start is the one moment deja is guaranteed to run on every
	// harness, which makes it the only reliable place to repair wiring left
	// behind by an older binary. It costs one small file read when nothing
	// changed.
	//
	// When it does change something it says so, once: the rewrite replaces
	// whatever those commands held, including a wrapper someone put there on
	// purpose, and silently reverting a person's edit is not a maintenance
	// note anybody can act on afterwards (#886).
	rewired := refreshWiringAfterUpgrade()
	// SessionStart fires for startup, resume, clear and compact; the payload
	// says which. After a compaction the model just lost its working context,
	// so the lead line changes to say the memory below survived it.
	var input struct {
		Source    string `json:"source"`
		SessionID string `json:"session_id"`
		// Cursor's sessionStart names the conversation conversation_id
		// (#3287); the once mark and the audit log key on it.
		ConversationID string `json:"conversation_id"`
		// The session that spawned this one, when the host knows it: an
		// opencode task session's digest led with its parent, which is live
		// and asking through it (#4548).
		ParentSessionID string `json:"parent_session_id"`
		TranscriptPath  string `json:"transcript_path"`
		CWD             string `json:"cwd"`
		// Cursor leaves cwd empty and names the project here instead.
		WorkspaceRoots []string `json:"workspace_roots"`
		// Grok spells all of this in camelCase, and so does Copilot CLI
		// (sessionId). See hook_grok.go.
		grokEnvelope
		// Once is set by a host whose session-start event is really a per-turn
		// one. OpenClaw's before_agent_start fires on every agent run, so
		// without this the project digest would go in front of the model on
		// every message rather than at the start of the session. The session
		// gets one attempt: the digest describes the project rather than the
		// question, so a first turn with nothing to say has nothing new to say
		// on the second either.
		Once bool `json:"deja_once"`
		// PromptStamps is set by a host whose prompt hook runs on every turn
		// a person asked for, so a digest call after the first is not what
		// says the session is in use. OpenClaw fires agent:bootstrap on its
		// heartbeat polls too, and the stamp they wrote hid the main DM from
		// recall for twenty minutes after each poll (#5270).
		PromptStamps bool `json:"deja_prompt_stamps"`
		// Shell is set by a host that cannot count on a deja tool for the
		// agent to call: pi before 0.99 has no MCP of its own, and a lead
		// naming recall_context sent the model to "Tool recall_context not
		// found" on every first turn (#4584).
		Shell bool `json:"deja_shell"`
		// The event that fired, when the payload names one. Devin sends
		// hook_event_name and rejects a reply that names a different event
		// back; Claude names it too, in the same canonical spelling.
		HookEventName string `json:"hook_event_name"`
		Harness       string `json:"harness"`
	}
	// Best effort, as every hook is — but not silent about it. A payload deja
	// cannot decode carries the session this injection went to, and losing it
	// without a word left the audit log unable to tell that case from a host
	// that sent nothing at all (#2161).
	payload := readHookStdin()
	unreadable := len(bytes.TrimSpace(payload)) > 0 && json.Unmarshal(payload, &input) != nil
	adoptCopilotHost(payload)
	adoptCursorHost(payload)
	input.SessionID = adoptGrok(adoptGrok(input.SessionID, input.grokEnvelope.SessionID), input.ConversationID)
	input.WorkspaceRoots = adoptGrokRoots(input.WorkspaceRoots, input.WorkspaceRoot)
	// The reply goes out under the event it arrived on, not under
	// SessionStart by right: Devin fires this hook on PostCompaction too,
	// and drops a reply that names the wrong event back (verified on
	// 3000.11.3). Only canonical names echo — hosts that spell their own
	// events here (Cursor's camelCase sessionStart, Gemini's BeforeAgent)
	// get the fallback they got before the echo existed.
	eventName := replyEventName(input.HookEventName, "SessionStart", "SessionStart", "PostCompaction")
	shape := hookToolClaude
	switch {
	case plain:
		shape = hookToolPlain
	case copilotHookOutput:
		shape = hookToolCopilot
	case mcodeHookOutput:
		shape = hookToolMcode
	}
	if input.Once && input.PromptStamps && sessionHadDigest(dir, input.SessionID) {
		return nil
	}
	// The first moment this session exists, so the first recall of it — the one
	// an agent plans against — already knows whose transcript to leave out.
	markSessionLive(dir, input.SessionID)
	// Grok shows the receipt and drops the context (#4588); the digest waits
	// for the session's first tool hook, which does reach the model
	// (hook_deferred.go).
	// CodeBuddy injects SessionStart context only while the input holds one
	// user message (isFirstOrResumeMessage, 2.161.2), and after a compaction it
	// holds the summary and the next prompt. Whatever this answered would be
	// marked delivered and never arrive, so the packet waits for the prompt
	// hook, which does reach the model (#4705).
	if input.Source == "compact" && sources.IsCodeBuddyTranscript(input.TranscriptPath) {
		return nil
	}
	// --once rides a per-prompt event beside hook-prompt, and Kimi runs the two
	// in parallel: both found the packet undelivered and it arrived twice. The
	// prompt hook carries it there.
	if !once {
		if delivered, err := emitCompactionRecovery(dir, input.SessionID, hookProjectPath(input.CWD, input.WorkspaceRoots), eventName, shape, os.Stdout); delivered {
			return err
		}
	}
	if once {
		input.Once = true
	}
	if input.Once {
		if sessionHadDigest(dir, input.SessionID) {
			return nil
		}
		rememberSessionDigest(dir, input.SessionID)
		// A Kimi /fork is a copy of its source under a new id, the digest
		// that opened the source among what it copied (#5146).
		if src := sources.KimiForkSource(input.SessionID); src != "" && sessionHadDigest(dir, src) {
			return nil
		}
	}
	// Claude Code forks with source "fork", and the fork carries its source's
	// turns, the digest that opened it among them. A second one led with the
	// source itself, the fork's own opening under another id (#4549).
	if input.Source == "fork" {
		// Its first prompt and tool come before the copy is written; they
		// learn the source from what this keeps (#5128).
		rememberClaudeForkSource(dir, input.SessionID, input.TranscriptPath)
		return nil
	}
	if threadHasDigest(dir, input.Source, input.SessionID, input.TranscriptPath) {
		return nil
	}
	input.WorkspaceRoots = adoptGrokRoots(input.WorkspaceRoots, input.WorkspaceRoot)
	// The harness tells us which project this is; deja read only the
	// environment, so a host that sends the payload without exporting
	// CLAUDE_PROJECT_DIR got no memory at all — indistinguishable from having
	// none (#759).
	// The session asking is left out of its own digest (#4199), except after a
	// compaction: the lead there points the agent at that session's id.
	var self []string
	lineage, cut := askerLineageCut(dir, input.SessionID, input.TranscriptPath, input.ParentSessionID)
	for id := range lineage {
		if input.Source == "compact" && id == input.SessionID {
			continue
		}
		self = append(self, id)
	}
	sort.Strings(self)
	var digest string
	var sessions, withheld int
	var raw int64
	var taskMatched, servedIDs, servedProjects []string
	if len(cut) > 0 {
		// A fork whose source went on: cut per asker, so not from the cache.
		e := hookDigestBuild(dir, hookProjectPath(input.CWD, input.WorkspaceRoots), self, false, cut)
		digest, sessions, raw, taskMatched, withheld, servedIDs, servedProjects = e.Digest, e.Sessions, e.Raw, e.TaskMatched, e.Withheld, e.IDs, e.Projects
	} else {
		digest, sessions, raw, taskMatched, withheld, servedIDs, servedProjects = cachedHookDigestFor(dir, hookProjectPath(input.CWD, input.WorkspaceRoots), self...)
	}
	var claudeMemCount int
	digest, claudeMemCount, servedProjects = attachClaudeAutoMemory(
		dir, input.SessionID, input.TranscriptPath, input.Harness,
		hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots)),
		digest, servedProjects,
	)
	if digest == "" {
		return hookContextWithoutDigest(dir, rewired, unreadable, plain, eventName, input.SessionID, withheld)
	}
	// One actionable line so injected memory leads somewhere: models that see
	// bare data tend to ignore it.
	lead := startLead(sessionStartLead)
	if input.Source == "compact" {
		lead = "Context was just compacted. The project memory below is from deja's index and survived the compaction; call recall_context with a session id from it to restore any details you lost.\n"
		// The generic digest is about the project. What a compacted session
		// most needs is its own evidence: measured on this corpus, a summary
		// keeps ~77% of the decisions and 0.2% of the commands that produced
		// them (#543).
		if ev := compactEvidence(dir, input.SessionID, hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots))); ev != "" {
			lead += "\n" + ev + "\n"
		}
	}
	if input.Shell {
		lead = shellRecallLead(lead)
	}
	digest = lead + digest
	if tip := limitHandoffTip(dir, hookCWD(hookProjectPath(input.CWD, input.WorkspaceRoots)), append(self, input.SessionID), func() string {
		return startingHarness(dir, input.SessionID, input.TranscriptPath)
	}); tip != "" {
		digest += "\n" + tip
	}
	digest = frameRecall(digest)
	if line := manualIndexLine(dir); line != "" {
		digest += "\n" + line
	}
	polName := policy.Load().Describe(policy.ActivationAuto)
	if unreadable {
		usage.RecordDigestPolicySessionsUnread(dir, usage.KindHook, digest, input.SessionID, sessions, raw, polName, servedIDs, servedProjects)
	} else {
		usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, digest, input.SessionID, sessions, raw, polName, servedIDs, servedProjects)
	}
	// What this project was told, so the next session start can say something
	// else. Without this the novelty ordering has nothing to read and every
	// start serves the same three sessions (#2038).
	// The agent session id is prefixed too, not just the project: the
	// per-prompt path bans a session it already showed *this* agent session,
	// and unprefixed rows made a session-start block count as that — the first
	// prompt about what the start just mentioned got nothing back.
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+input.SessionID, hookProjectKey(hookProjectPath(input.CWD, input.WorkspaceRoots)), servedIDs)
	if plain {
		fmt.Fprintln(os.Stdout, digest)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = eventName
	resp.HookSpecificOutput.AdditionalContext = digest
	resp.SystemMessage = hookSessionStartReceipt(dir, digest, rewired, sessions, claudeMemCount, withheld, polName, taskMatched)
	// Nothing below is spent on a host that shows it to nobody.
	//
	// A build under way is said whether or not there is a receipt. Gating it on
	// an empty message covered the session with nothing to recall and left out
	// the one that has something: the digest there came from the cache, which
	// is the user's own history and right to serve (#874), while the store
	// behind it cannot answer a term until the build lands. What that session
	// printed was "the agent starts already knowing them" and nothing else
	// (#2697) — the silence #927 fixed on the environment-facts path, reached
	// from the other side.
	if notesHaveReader() {
		if note := buildNotice(dir); note != "" {
			if resp.SystemMessage == "" {
				resp.SystemMessage = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring), note))
			} else {
				resp.SystemMessage = joinNotes(resp.SystemMessage, note)
			}
		}
		if note := unreadableStoreNote(dir); storeNoteIsNews(dir, note) {
			resp.SystemMessage = joinNotes(note, resp.SystemMessage)
		}
		// What the build found, once (#3073), then once a week what the week
		// looked like (#3065) — after everything that needs acting on.
		resp.SystemMessage = joinNotes(resp.SystemMessage, humanNotes(dir))
		// An index that is behind and cannot be written stays behind. "the agent
		// starts already knowing them" was printed over a picture missing today's
		// work, and this path said nothing — search names the same state (#1005).
		if note := staleReadOnlyNote(dir); note != "" {
			resp.SystemMessage = joinNotes(note, resp.SystemMessage)
		}
	}
	emitHookResponse(resp)
	return nil
}

// hookContextWithoutDigest answers a session start that found no session from
// this project: the machine's environment block, or a word about the build.
func hookContextWithoutDigest(dir string, rewired []string, unreadable, plain bool, eventName, sessionID string, withheld int) error {
	// No session from this project, which is the usual state in a new
	// checkout — and exactly where knowing what this machine is missing
	// helps most. The block is about the machine, not the project, so it
	// does not depend on the digest having found anything.
	// The block answers for the switch itself since #2701 — the guard that
	// stood here is what taught it to.
	if env, from := environmentBlockFrom(dir, policy.ActivationAuto); env != "" {
		out := frameRecall(env)
		// The block is about the machine and names no project, so without
		// the projects behind its walls a forget of one of them could not
		// reach the stored text (#2349).
		if unreadable {
			usage.RecordDigestPolicySessionsUnread(dir, usage.KindHook, out, sessionID, 0, 0, policy.Load().Describe(policy.ActivationAuto), nil, from)
		} else {
			usage.RecordDigestPolicyFrom(dir, usage.KindHook, out, sessionID, 0, 0, from, policy.Load().Describe(policy.ActivationAuto))
		}
		if plain {
			fmt.Fprintln(os.Stdout, out)
			return nil
		}
		var resp sessionStartHookResponse
		resp.HookSpecificOutput.HookEventName = eventName
		resp.HookSpecificOutput.AdditionalContext = out
		// The environment block is not the project's memory, and while a
		// build runs it is all there is: without this the whole rebuild
		// passed in silence on any machine with facts to report (#927).
		if notesHaveReader() {
			resp.SystemMessage = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring), joinNotes(withheldEverythingNote(dir, withheld), buildNotice(dir))))
			// Last, after anything that needs acting on: what the build
			// found, once (#3073), then once a week what the week looked
			// like (#3065).
			resp.SystemMessage = joinNotes(resp.SystemMessage, humanNotes(dir))
		}
		emitHookResponse(resp)
		return nil
	}
	// A first build (or one forced by a new index format) is running in
	// the background. It never blocks the agent, but starting in silence
	// looks like deja is simply not working — so say what is happening.
	// Only on the JSON path: systemMessage is the host's UI channel,
	// whereas the plain path is injected into the model's context, where
	// a progress line is noise.
	if !plain && notesHaveReader() {
		// The session that asks for the build is the one that hears
		// nothing about it: the child has not written its first progress
		// line yet. That session is the first one after an upgrade or a
		// damaged store — the moment deja most looks broken (#878).
		//
		// Including the very first build. Requiring a manifest left the
		// one moment deja most looks broken in silence: a machine with
		// ten thousand transcripts and no index yet said nothing at all,
		// twice over, while the build ran (#909).
		//
		// A machine with no history also asks for a build; it sees this
		// line once, truthfully, and never again — the next session has a
		// manifest and reads the published progress instead.
		line := buildNotice(dir)
		if note := unreadableStoreNote(dir); storeNoteIsNews(dir, note) {
			line = joinNotes(note, line)
		}
		line = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring), joinNotes(withheldEverythingNote(dir, withheld), line)))
		if line != "" {
			var resp sessionStartHookResponse
			resp.HookSpecificOutput.HookEventName = eventName
			resp.SystemMessage = line
			emitHookResponse(resp)
		}
	}
	return nil
}

// unreadableStoreNote names a store deja could not read in full. A newcomer's
// first contact is the agent, not the CLI: `deja index` and `deja doctor` both
// explain a locked directory, and neither is what someone who installed deja
// and went back to their agent ever runs, so the project simply had no memory
// and nothing said why (#917). The count is already in the manifest, so this
// costs one read of what the hook path has read anyway.
func unreadableStoreNote(dir string) string {
	var names []string
	total := 0
	health := index.IngestHealth(dir)
	for _, h := range sortedHarnesses(health) {
		e := health[h]
		if e.FailedFiles == 0 {
			continue
		}
		total += e.FailedFiles
		name := h
		if h == "deja" {
			name = "your notes"
		}
		names = append(names, name)
	}
	if total == 0 {
		return ""
	}
	return fitWarning(
		fmt.Sprintf("deja could not read %d path%s in %s, so recall misses them — `deja doctor` names %s",
			total, pluralS(total), strings.Join(names, ", "), pluralWhich(total)),
		fmt.Sprintf("deja could not read %d path%s in %s — `deja doctor` names %s",
			total, pluralS(total), fitList(names, 40), pluralWhich(total)))
}

// storeNoteIsNews keeps the line above from becoming wallpaper: it is repeated
// only when the count changes, or once a day while it stands.
func storeNoteIsNews(dir, note string) bool {
	return noteIsNews(dir+".storenote", note)
}

// withheldEverythingNote is what session start says when the trust policy hid
// every session this project had. The receipt reports a rule that withheld
// something only on the path where memory also landed; when the rule hid all
// of it the hook went out silent, which reads exactly like a project with no
// history — and the one state where the fix is a config line the user owns.
func withheldEverythingNote(dir string, withheld int) string {
	if withheld == 0 {
		return ""
	}
	note := withheldNote(policy.Load().Describe(policy.ActivationAuto), withheld)
	if !noteIsNews(dir+".policynote", note) {
		return ""
	}
	return note
}

// withheldNote is the line itself, for a policy and a count.
func withheldNote(pol string, withheld int) string {
	return fitWarning(
		fmt.Sprintf("deja recalled nothing: policy %s withheld %s here — `deja doctor` shows the rule", pol, doctorCount(withheld, "session")),
		fmt.Sprintf("deja: policy %s withheld %s here — `deja doctor` shows it", pol, doctorCount(withheld, "session")))
}

// noteIsNews reports whether a line is worth repeating: only when its text
// changes, or once a day while it stands.
func noteIsNews(p, note string) bool {
	if note == "" {
		return false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(note))
	sum := strconv.FormatUint(h.Sum64(), 16)
	if b, err := os.ReadFile(p); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) == 2 && parts[0] == sum {
			if ts, err := strconv.ParseInt(parts[1], 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < 24*time.Hour {
				return false
			}
		}
	}
	_ = os.WriteFile(p, []byte(sum+" "+strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

// receiptIsNews reports whether this digest differs from the one last
// announced (per index, 24h window). Best-effort: on any error, announce.
func receiptIsNews(dir, digest string) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(digest))
	sum := strconv.FormatUint(h.Sum64(), 16)
	p := dir + ".receipt"
	if b, err := os.ReadFile(p); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) == 2 && parts[0] == sum {
			if ts, err := strconv.ParseInt(parts[1], 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < 24*time.Hour {
				return false
			}
		}
	}
	_ = os.WriteFile(p, []byte(sum+" "+strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

// on its own environment, and the warmup child not at all, since `deja index`
// reads every project. What the write still did was carry one call's project
// into the next in the same process, which is the fault #2182 was, and which
// made an earlier measurement of #2161 report the opposite of the truth
// (#2185).
func hookCWD(fromPayload string) string {
	if fromPayload != "" {
		return fromPayload
	}
	if cwd := os.Getenv("CLAUDE_PROJECT_DIR"); cwd != "" {
		return cwd
	}
	// Devin carries no cwd in a hook payload; its launcher sets
	// DEVIN_PROJECT_DIR for the hook's own process instead (3000.11.3).
	if cwd := os.Getenv("DEVIN_PROJECT_DIR"); cwd != "" {
		return cwd
	}
	cwd, _ := os.Getwd()
	return cwd
}

// notice. Claude Code prints "You've hit your session limit · resets 2:40am",
// "You've hit your limit · resets 3pm", "You've reached your Fable limit" and
// "API Error: 529 Overloaded" (#4758); the old list knew only the weekly form.
func limitNotice(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > 300 {
		return false
	}
	if (strings.HasPrefix(t, "You've hit your ") || strings.HasPrefix(t, "You've reached your ")) && strings.Contains(t, "limit") {
		return true
	}
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "api error: ") && (strings.Contains(lower, "529") || strings.Contains(lower, "overloaded")) {
		return true
	}
	for _, marker := range limitMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// limitHandoffWindow is how long after a limit the note still applies.
const limitHandoffWindow = 6 * time.Hour

// limitHandoffTip checks whether the newest session of this project from
// another harness ended on a usage limit and, if so, suggests continuing via
// handoff. The newest session on the machine was the wrong one to read: any
// parallel session, subagent or the starting session itself pushed the
// limited one out (#4758). starting names the harness of the session that is
// starting; it is asked only when there is a fresh candidate to rule on.
func limitHandoffTip(dir, cwd string, self []string, starting func() string) string {
	// Manifest rows inside the window first: loading every recent session of
	// the project cost 265 ms of a session start on a real store, for a note
	// that almost never applies.
	since := time.Now().Add(-limitHandoffWindow)
	metas, err := index.RecentProjectMetasUnder(dir, digest.ProjectNameCandidates(cwd), cwd, since, 12)
	if err != nil || len(metas) == 0 {
		return ""
	}
	skip := make(map[string]bool, len(self))
	for _, id := range self {
		skip[id] = true
	}
	pol := policy.Load()
	var fresh []index.SessionMeta
	for _, m := range metas {
		if skip[m.ID] || sources.HarnessExcluded(m.Harness) || !pol.Allows(policy.ActivationAuto, m.Project) {
			continue
		}
		fresh = append(fresh, m)
	}
	if len(fresh) == 0 {
		return ""
	}
	harness := ""
	if starting != nil {
		harness = starting()
	}
	var pick *index.SessionMeta
	for i := range fresh {
		if harness != "" && fresh[i].Harness == harness {
			continue
		}
		if pick == nil || fresh[i].Updated.After(pick.Updated) {
			pick = &fresh[i]
		}
	}
	if pick == nil {
		return ""
	}
	s, ok, err := index.FindByIdentity(dir, pick.Harness, pick.ID)
	if err != nil || !ok {
		return ""
	}
	tail := s.Messages
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	for _, m := range tail {
		if m.Role != "user" && limitNotice(m.Text) {
			return fmt.Sprintf("Note: the most recent %s session in this project appears to have hit a usage limit. If that blocks the user again, suggest `deja handoff --to <agent>` — it packages the live context and continues the work in another agent.", s.Harness)
		}
	}
	return ""
}

func attachClaudeAutoMemory(dir, id, transcript, harness, cwd, digest string, projects []string) (string, int, []string) {
	if isClaudeSessionStart(dir, id, transcript, harness) {
		return digest, 0, projects
	}
	cm, n, proj := claudeAutoMemoryBlock(cwd)
	if cm == "" {
		return digest, 0, projects
	}
	if digest == "" {
		digest = strings.TrimRight(cm, "\n")
	} else {
		digest = cm + "\n" + digest
	}
	if proj != "" && !slices.Contains(projects, proj) {
		projects = append(projects, proj)
	}
	return digest, n, projects
}

func hookSessionStartReceipt(dir, digest string, rewired []string, sessions, claudeMemCount, withheld int, polName string, taskMatched []string) string {
	if (sessions == 0 && claudeMemCount == 0) || !notesHaveReader() || !receiptIsNews(dir, digest) {
		return ""
	}
	polNote := ""
	if polName != "local+imported" {
		polNote = " · policy: " + polName
		if sessions > 0 && withheld > 0 {
			polNote += fmt.Sprintf(" (%s withheld here)", doctorCount(withheld, "session"))
		}
	}
	if sessions == 0 {
		return joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring),
			receiptLine(fmt.Sprintf("deja carried over %d Claude auto-memory note%s from this project%s", claudeMemCount, pluralS(claudeMemCount), polNote),
				reusedMemory{}, serviceReceipt(dir))))
	}
	why := "from this project"
	if len(taskMatched) > 0 {
		why = "touching " + fitList(taskMatched, 48)
	}
	var reused reusedMemory
	hookPol := policy.Load()
	if r, ok := findReusedMemory(dir, func(project string) bool {
		return hookPol.Allows(policy.ActivationAuto, project)
	}); ok {
		reused = r
	}
	return joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring),
		receiptLine(fmt.Sprintf("deja recalled %d prior session%s %s%s", sessions, pluralS(sessions), why, polNote),
			reused, serviceReceipt(dir))))
}

// isClaudeSessionStart reports whether a SessionStart hook payload comes from
// Claude Code itself, which already loads ~/.claude/projects/<encoded>/memory
// natively and should not receive those entries twice (#5095).
func isClaudeSessionStart(dir, id, transcript, harness string) bool {
	if h := strings.ToLower(strings.TrimSpace(harness)); h != "" {
		return h == "claude"
	}
	if startingHarness(dir, id, transcript) == "claude" {
		return true
	}
	if transcript == "" {
		return false
	}
	if sources.UnderClaudeRoot(transcript) {
		return true
	}
	slash := filepath.ToSlash(filepath.Clean(transcript))
	return strings.Contains(slash, "/.claude/projects/") || strings.Contains(slash, "/.claude/transcripts/")
}

// startingHarness is the harness of the session a hook fires for, or "" when
// nothing says: its transcript's head, where that transcript lives, or the
// index for a session that is resuming.
func startingHarness(dir, id, transcript string) string {
	if id == "" {
		return ""
	}
	if h, ok := askerHead(id, transcript); ok && h.Harness != "" {
		return h.Harness
	}
	if transcript != "" {
		if sources.IsCodeBuddyTranscript(transcript) {
			return "codebuddy"
		}
		if sources.UnderClaudeRoot(transcript) {
			return "claude"
		}
	}
	if s, ok, err := index.FindByPrefix(dir, id); err == nil && ok && s.ID == id {
		return s.Harness
	}
	return ""
}

// serviceReceipt appends today's tally when there is one — the moment memory
// lands is the moment to say what it has been doing all day.
func serviceReceipt(dir string) string {
	// The same two counts the statusline prints, and the same names for them:
	// what an agent asked for and got is a recall, and what deja sent unasked
	// is memory arriving. Folding the second into the first made the receipt
	// and the statusline disagree in one session, and the receipt is the one
	// that rides into the model's context (#1575, following #1569).
	n := usage.StatusCounters(dir)
	served, bytes := n.Recalls, n.Bytes+n.Injected
	if served+n.Injections < 2 || bytes == 0 {
		return ""
	}
	var parts []string
	if served > 0 {
		parts = append(parts, fmt.Sprintf("%d recall%s", served, pluralS(served)))
	}
	if n.Injections > 0 {
		parts = append(parts, "memory arrived "+timesToday(n.Injections))
	}
	// The bytes stay in the gate above and out of the line: "3.1 KB distilled
	// from 40 KB" was a number nobody acted on, and it pushed the receipt
	// onto a second line. `deja stats` has it.
	return " · today: " + strings.Join(parts, ", ")
}

// timesToday counts arrivals in words, so the receipt reads as a sentence
// rather than a gauge: "memory arrived twice" beats "memory arrived 2 times".
func timesToday(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	}
	return fmt.Sprintf("%d times", n)
}

// staleReadOnlyNote is the one line for an index that is behind and cannot
// catch up: the store has newer sessions and the directory the rebuild would
// write is not writable. Costs one manifest stat plus one temp-file probe, and
// only when the index is already known to be behind.
func staleReadOnlyNote(dir string) string {
	if indexCanCatchUp(dir) {
		return ""
	}
	return staleIndexNote(filepath.Dir(dir))
}

// staleIndexNote is that line for the directory that is not writable.
func staleIndexNote(parent string) string {
	return fitWarning(
		fmt.Sprintf("deja is serving a stale index that cannot be updated — %s is not writable", parent),
		fmt.Sprintf("deja's index cannot be updated: %s is not writable", parent))
}

// indexCanCatchUp reports whether the index is either current or able to
// become current. False only for the one state search already names: newer
// sessions on disk and nowhere to write the result.
func indexCanCatchUp(dir string) bool {
	// Writability first: it is one temp file, while UpToDate walks every
	// store. Measured on 6000 sessions, asking the expensive question first
	// cost 25ms on every session start; this way the ordinary machine pays
	// nothing (#1005).
	if indexDirWritable(dir) {
		return true
	}
	if !index.HasManifest(dir) {
		return true
	}
	fresh, _ := index.UpToDate(dir, "")
	return fresh
}

// startLead swaps a "from this project" lead for the wide one when recall is
// set to reach past the checkout. The mode replaces the lookup names with the
// projects of the machine's recent sessions (see hookDigestResultFor), and
// every harness lead said "this project" either way — so a client's sessions
// arrived described as this project's history (#2343).
func startLead(narrow string) string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DEJA_RECALL")), search.RecallAggressive) {
		return wideRecallLead
	}
	return narrow
}

// shellRecallLead points a lead at the shell command instead of the tool, for
// a harness whose agent has no deja tool to call (#4584).
func shellRecallLead(lead string) string {
	return strings.ReplaceAll(lead, "call recall_context with", "run `deja ctx` in the shell with")
}

// wideRecallLead is sessionStartLead for DEJA_RECALL=aggressive, where the
// sessions come from this machine rather than from this checkout.
const wideRecallLead = "The sessions below are recent work on this machine, not only in this project — deja is set to recall widely. If any is relevant to what the user asks next, call recall_context with the session id printed beside it — an id is exact, a phrase is a guess — to pull the full details before acting. \nIf one of these helps, open your reply with that one line and nothing more about it: déjà vu: <what it said> — reusing it (deja:<session id>). If none helps, say nothing about them.\n"

// sessionStartMarker is how a start digest opens, under either lead;
// sessionStartRuleMarker is how one that carries a user rule opens.
const (
	sessionStartMarker     = recallFrameHeader + "The sessions below are "
	sessionStartRuleMarker = recallRuleFrameHeader + "The sessions below are "
)

const sessionStartLead = "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call recall_context with the session id printed beside it — an id is exact, a phrase is a guess — to pull the full details before acting. \nIf one of these helps, open your reply with that one line and nothing more about it: déjà vu: <what it said> — reusing it (deja:<session id>). If none helps, say nothing about them.\n"

// dropHookCaches removes every cached session-start digest beside this index.
// They are keyed by working directory, so there is one per project a hook has
// ever run in, and they hold the block as prose (#3411).
func dropHookCaches(dir string) {
	matches, err := filepath.Glob(dir + ".hookcache-*")
	if err != nil {
		return
	}
	// The first cache deja ever wrote had no suffix; a machine that has run
	// hooks since then still carries it.
	matches = append(matches, dir+".hookcache")
	for _, p := range matches {
		_ = os.Remove(p)
		_ = os.Remove(p + ".refreshing")
	}
	// The marks for the sessions being written go with them: a forget that left
	// them behind would keep hiding whatever they name for the rest of the
	// window, and after a forget there is nothing left to hide.
	_ = os.Remove(liveSessionsPath(dir))
}
