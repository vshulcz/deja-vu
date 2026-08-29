package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

type precompactHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Trigger        string `json:"trigger"`
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
func runHookPrecompact(dir string) {
	var input precompactHookInput
	_ = json.Unmarshal(readHookStdin(), &input)
	// Compaction throws away the blocks this session was shown, and the list
	// that stops them repeating outlives them — so the memory the agent just
	// lost is exactly the memory recall refuses to send again. Forget what this
	// session was shown; everything else in the file belongs to other sessions.
	forgetInjected(dir, input.SessionID)
	requestWarmup(dir)
}

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
	return fmt.Sprintf("deja could not rewire %s — run `deja install %s` to see why; until then this line comes back every session",
		strings.Join(targets, ", "), targets[0])
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
	return fmt.Sprintf("deja rewrote its wiring for %s after an upgrade — `deja install` is what writes those commands", strings.Join(targets, ", "))
}

// buildNotice is what a session start says while a build runs: the published
// progress if there is any, the bare promise if the build was only just asked
// for, and the reason instead when the index cannot be written at all. It is
// shared with the environment-facts path, which returned before ever reaching
// it — so a machine that had environment facts heard nothing at all through an
// entire post-upgrade rebuild, while the statusline showed the progress bar
// (#927).
func buildNotice(dir string) string {
	if st := readWarmupStatus(dir); st != nil {
		// On a machine no agent has written to, "indexing your history —
		// recall comes online when it finishes" is deja's first word to an
		// agent and none of it is so: there is nothing to read, and recall
		// starts when some agent writes a transcript, not in a few seconds
		// (#2407). Same claim the CLI makes about the same machine. A build
		// that has counted work to do is believed over the store walk: it is
		// reading something, whatever a later look at the stores finds.
		if st.Total <= 0 && noAgentHistoryFound() {
			return "deja: no agent history was found on this machine yet — recall starts once an agent writes a session here; `deja sources` shows where deja looked"
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
			return fmt.Sprintf("deja cannot find the index (%s) — the disk it lives on may have been unmounted; reconnect it, or point DEJA_INDEX_DIR somewhere local", parent)
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
			return "deja: the index cannot answer — recall is quiet until `deja index` rebuilds it"
		}
		return fmt.Sprintf("deja needs to rebuild the index and %s is not writable — `deja index` says what to change", unwritableIndexDir(dir))
	}
	if !warmupJustRequested(dir) {
		return ""
	}
	return "deja is indexing your history — recall comes online when it finishes"
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
		CWD       string `json:"cwd"`
	}
	_ = json.Unmarshal(readHookStdin(), &input)
	// The harness tells us which project this is; deja read only the
	// environment, so a host that sends the payload without exporting
	// CLAUDE_PROJECT_DIR got no memory at all — indistinguishable from having
	// none (#759).
	digest, sessions, raw, taskMatched, withheld, servedIDs, servedProjects := cachedHookDigestFor(dir, input.CWD)
	if digest == "" {
		// No session from this project, which is the usual state in a new
		// checkout — and exactly where knowing what this machine is missing
		// helps most. The block is about the machine, not the project, so it
		// does not depend on the digest having found anything.
		if env, from := environmentBlockFrom(dir, policy.ActivationAuto); env != "" {
			out := frameRecall(env)
			// The block is about the machine and names no project, so without
			// the projects behind its walls a forget of one of them could not
			// reach the stored text (#2349).
			usage.RecordDigestPolicyFrom(dir, usage.KindHook, out, input.SessionID, 0, 0, from, policy.Load().Describe(policy.ActivationAuto))
			if plain {
				fmt.Fprintln(os.Stdout, out)
				return nil
			}
			var resp sessionStartHookResponse
			resp.HookSpecificOutput.HookEventName = "SessionStart"
			resp.HookSpecificOutput.AdditionalContext = out
			// The environment block is not the project's memory, and while a
			// build runs it is all there is: without this the whole rebuild
			// passed in silence on any machine with facts to report (#927).
			resp.SystemMessage = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring), joinNotes(withheldEverythingNote(dir, withheld), buildNotice(dir))))
			if b, err := json.Marshal(resp); err == nil {
				fmt.Fprintln(os.Stdout, string(b))
			}
			return nil
		}
		// A first build (or one forced by a new index format) is running in
		// the background. It never blocks the agent, but starting in silence
		// looks like deja is simply not working — so say what is happening.
		// Only on the JSON path: systemMessage is the host's UI channel,
		// whereas the plain path is injected into the model's context, where
		// a progress line is noise.
		if !plain {
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
				resp.HookSpecificOutput.HookEventName = "SessionStart"
				resp.SystemMessage = line
				if b, err := json.Marshal(resp); err == nil {
					fmt.Fprintln(os.Stdout, string(b))
				}
			}
		}
		return nil
	}
	// One actionable line so injected memory leads somewhere: models that see
	// bare data tend to ignore it.
	lead := startLead(sessionStartLead)
	if input.Source == "compact" {
		lead = "Context was just compacted. The project memory below is from deja's index and survived the compaction; call recall_context with a term from it to restore any details you lost.\n"
		// The generic digest is about the project. What a compacted session
		// most needs is its own evidence: measured on this corpus, a summary
		// keeps ~77% of the decisions and 0.2% of the commands that produced
		// them (#543).
		if ev := compactEvidence(dir, input.SessionID, hookCWD(input.CWD)); ev != "" {
			lead += "\n" + ev + "\n"
		}
	}
	digest = lead + digest
	if tip := limitHandoffTip(dir); tip != "" {
		digest += "\n" + tip
	}
	digest = frameRecall(digest)
	polName := policy.Load().Describe(policy.ActivationAuto)
	usage.RecordDigestPolicySessionsFrom(dir, usage.KindHook, digest, input.SessionID, sessions, raw, polName, servedIDs, servedProjects)
	// What this project was told, so the next session start can say something
	// else. Without this the novelty ordering has nothing to read and every
	// start serves the same three sessions (#2038).
	// The agent session id is prefixed too, not just the project: the
	// per-prompt path bans a session it already showed *this* agent session,
	// and unprefixed rows made a session-start block count as that — the first
	// prompt about what the start just mentioned got nothing back.
	rememberInjectedIDsFor(dir, sessionStartKeyPrefix+input.SessionID, hookProjectKey(input.CWD), servedIDs)
	if plain {
		fmt.Fprintln(os.Stdout, digest)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = digest
	// Announce only when the recalled set changed since the last announcement:
	// injection is recency-ranked, so repeating the same receipt every session
	// start is wallpaper, and wallpaper builds no habit.
	if sessions > 0 && receiptIsNews(dir, digest) {
		plural := ""
		if sessions > 1 {
			plural = "s"
		}
		// The receipt says why these sessions and not just "recent": when the
		// working tree pointed at them, name the files that did it.
		why := "from this project"
		if len(taskMatched) > 0 {
			why = "touching " + strings.Join(taskMatched, ", ")
		}
		// A non-default policy is part of the receipt: the user should see
		// that a rule, not chance, decided what memory crossed over.
		polNote := ""
		if polName != "local+imported" {
			polNote = " · policy: " + polName
			// The line was identical whether the rule hid a session here or
			// merely existed, so the reader could not tell that memory had
			// been withheld from this very session — search has said it since
			// the counter existed (#L-new19).
			if withheld > 0 {
				polNote += fmt.Sprintf(" (%s withheld here)", doctorCount(withheld, "session"))
			}
		}
		// A count says deja did something; a name says what. The receipt is
		// the one place a person reliably sees, so when a piece of work has
		// earned its keep more than once, it is named here rather than only
		// on a screen someone has to decide to open (#579).
		earned := ""
		hookPol := policy.Load()
		if r, ok := findReusedMemory(dir, func(project string) bool {
			return hookPol.Allows(policy.ActivationAuto, project)
		}); ok {
			earned = fmt.Sprintf(" · most re-used recently: %q, %d×", trimBriefTitle(r.Title), r.Times)
		}
		// The receipt is read at a glance, and several hosts show it in a
		// toast about fifty columns wide. At 130 characters it wrapped to
		// three lines over the conversation, splitting "2.4 KB" across two of
		// them. What it says has to survive that, so the claim leads and the
		// numbers trail, where a wrap costs least.
		//
		// The clause explaining what a recall is has a job exactly once. The
		// service line only appears from the second recall of the day, so its
		// presence is the signal that this is no longer news.
		svc := serviceReceipt(dir)
		teaching := " — the agent starts already knowing them"
		if svc != "" {
			teaching = ""
		}
		// No colon after the name: hosts introduce the line themselves, and
		// Claude Code's is "SessionStart:startup says:", which turned the
		// receipt into "says: deja: recalled …" on screen. Without it the
		// sentence reads whole after any host's prefix and still carries the
		// name for hosts that add none.
		resp.SystemMessage = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring),
			fmt.Sprintf("deja recalled %d prior session%s %s%s%s%s%s",
				sessions, plural, why, teaching, svc, polNote, earned)+
				fmt.Sprintf(" · %s of context", humanBytes(int64(len(digest))))))
	}
	// Nothing to recall yet because the index is still being built: say so
	// rather than starting in silence. The build runs detached, so the agent
	// is already usable — this only explains why memory is not here yet.
	if resp.SystemMessage == "" {
		if st := readWarmupStatus(dir); st != nil {
			resp.SystemMessage = joinNotes(rewireNote(rewired), joinNotes(stuckWiringNote(stuckWiring), st.line()))
		}
	}
	if note := unreadableStoreNote(dir); storeNoteIsNews(dir, note) {
		resp.SystemMessage = joinNotes(note, resp.SystemMessage)
	}
	// An index that is behind and cannot be written stays behind. "the agent
	// starts already knowing them" was printed over a picture missing today's
	// work, and this path said nothing — search names the same state (#1005).
	if note := staleReadOnlyNote(dir); note != "" {
		resp.SystemMessage = joinNotes(note, resp.SystemMessage)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(os.Stdout, string(b))
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
	return fmt.Sprintf("deja could not read %d path%s in %s — sessions are missing from recall; `deja doctor` names %s",
		total, pluralS(total), strings.Join(names, ", "), pluralWhich(total))
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
	note := fmt.Sprintf("deja recalled nothing here — the trust policy (%s) withheld %s from this project; `deja doctor` shows the rule",
		policy.Load().Describe(policy.ActivationAuto), doctorCount(withheld, "session"))
	if !noteIsNews(dir+".policynote", note) {
		return ""
	}
	return note
}

// noteIsNews reports whether a line is worth repeating: only when its text
// changes, or once a day while it stands.
func noteIsNews(p, note string) bool {
	if note == "" {
		return false
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(note))
	sum := fmt.Sprintf("%x", h.Sum64())
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
	sum := fmt.Sprintf("%x", h.Sum64())
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

// hookDigestTTL bounds how long a cached session-start digest is considered
// fresh. Older entries are still served instantly — startup must never wait
// on digest work — but a detached refresh is kicked so the next session gets
// a current one (stale-while-revalidate).
// A
// minute-old digest is indistinguishable at session start, and the cache
// turns the common hook path from ~120ms of index work into one file read.
const hookDigestTTL = 60 * time.Second

type hookCacheEntry struct {
	At  time.Time `json:"at"`
	CWD string    `json:"cwd"`
	// Gate records the recall mode and the policy the digest was built
	// under. A cache hit returns before either is consulted, so serving an
	// entry built under different rules would let a cached digest outlive
	// DEJA_RECALL=off or a policy that now forbids it.
	Gate     string `json:"gate,omitempty"`
	Digest   string `json:"digest"`
	Sessions int    `json:"sessions"`
	// Withheld counts the candidates the trust policy dropped for this
	// digest. Old entries decode as zero, which reads as "nothing withheld"
	// and is what they meant.
	Withheld    int      `json:"withheld,omitempty"`
	Raw         int64    `json:"raw"`
	TaskMatched []string `json:"task_matched,omitempty"`
	// Projects names the projects behind the digest, so the injection log can
	// be held to a rule or a forget without reading its prose (#2349). Old
	// entries decode as none, which is what they carried.
	Projects []string `json:"projects,omitempty"`
	// IDs are the sessions this digest carries, so a cache hit logs the same
	// thing a fresh build does. Without them the session-start hook was the
	// one surface whose repetition could not be counted at all: 606 injections
	// in six weeks with no record of what was in them (#2038). Old entries
	// decode as nil and log nothing, which is what they knew.
	IDs []string `json:"ids,omitempty"`
}

func hookCachePath(dir, cwd string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(cwd))
	return fmt.Sprintf("%s.hookcache-%08x", dir, h.Sum32())
}

// hookGate identifies the rules a digest was built under: the recall mode and
// the auto-activation policy. Both are consulted only deep inside
// hookDigestResult, which a cache hit never reaches.
func hookGate() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL")))
	// The shape of the entry is part of what it was built under: one written
	// before the digest recorded its projects would be served for its whole
	// TTL and logged as a digest that came from nowhere (#2349).
	//
	// And the notes file, because the block leads with the decisions promoted
	// in this project. Without it a decision the reader had just removed with
	// `deja forget` kept being handed to every session start until the TTL ran
	// out — search agreed it was gone and the block went on quoting it, which
	// is the one thing the privacy command must not do (#2537). A promotion
	// made a moment ago has the same problem in reverse. Stat, not read.
	return "v3|" + mode + "|" + policy.Load().Describe(policy.ActivationAuto) + "|" + notesStamp()
}

// notesStamp identifies the notes file as it is now. A file that cannot be
// stat'd stamps as absent, which is itself a state worth telling apart from a
// file with content in it.
func notesStamp() string {
	fi, err := os.Stat(sources.NotesFile())
	if err != nil {
		return "none"
	}
	return strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// hookCWD is where the hook is standing: what the payload says, else the
// project directory the harness exported, else the process's own — so a host
// that exports the directory without naming it in the payload is not treated
// as standing nowhere (#759), and one that names it in the payload without
// exporting anything is not either.
//
// deja used to write the payload's answer back into its own environment. After
// #2183 nothing read it to decide anything: the doors take the project from the
// payload, and the two children that need one are handed it — the refresh child
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
	cwd, _ := os.Getwd()
	return cwd
}

func cachedHookDigest(dir string) (string, int, int64, []string, int, []string, []string) {
	return cachedHookDigestFor(dir, "")
}

// cachedHookDigestFor is cachedHookDigest for a caller that was told which
// project the call is about. The payload is that authority: deja used to write
// it into its own environment and read it back, which answered a second payload
// in the same process with the first one's project (#2182, #2185).
func cachedHookDigestFor(dir, fromPayload string) (string, int, int64, []string, int, []string, []string) {
	cwd := hookCWD(fromPayload)
	if strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL"))) == search.RecallOff {
		return "", 0, 0, nil, 0, nil, nil
	}
	// Before the cache read: a hit returns without reaching the version guard
	// in hookDigestResult, so an index left behind by an upgrade was served
	// stale and never rebuilt (#777). The cached digest is still the user's
	// own history, so serve it — just ask for the rebuild too.
	// A deleted index is the same situation with the manifest gone: the cache
	// answered from a store that no longer exists and asked for nothing, so it
	// kept serving that snapshot forever — every session recorded after the
	// deletion invisible to the hook (#874). Caches do get wiped: ~/.cache is
	// fair game for cleanup tools and CI images.
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
	}
	gate := hookGate()
	p := hookCachePath(dir, cwd)
	if b, err := os.ReadFile(p); err == nil {
		var e hookCacheEntry
		if json.Unmarshal(b, &e) == nil && e.Digest != "" && e.CWD == cwd && e.Gate == gate {
			if time.Since(e.At) >= hookDigestTTL {
				// Serve stale instantly; a detached self-refresh rebuilds
				// the cache off the startup path.
				requestHookRefresh(dir, cwd)
			}
			return e.Digest, e.Sessions, e.Raw, e.TaskMatched, e.Withheld, e.IDs, e.Projects
		}
	}
	digest, sessions, raw, taskMatched, withheld, ids, projects := hookDigestResultFor(dir, cwd)
	writeHookCache(dir, cwd, digest, sessions, raw, taskMatched, withheld, ids, projects)
	return digest, sessions, raw, taskMatched, withheld, ids, projects
}

func writeHookCache(dir, cwd, digest string, sessions int, raw int64, taskMatched []string, withheld int, ids, projects []string) {
	if digest == "" {
		return
	}
	if b, err := json.Marshal(hookCacheEntry{At: time.Now(), CWD: cwd, Gate: hookGate(), Digest: digest, Sessions: sessions, Raw: raw, TaskMatched: taskMatched, Withheld: withheld, IDs: ids}); err == nil {
		_ = os.WriteFile(hookCachePath(dir, cwd), b, 0o600)
	}
}

// requestHookRefresh spawns a detached `deja hook-refresh` for cwd; a
// same-named sentinel prevents stampedes. Best effort by design.
func requestHookRefresh(dir, cwd string) {
	if os.Getenv("DEJA_HOOK_REFRESH") != "" {
		return
	}
	sentinel := hookCachePath(dir, cwd) + ".refreshing"
	if fi, err := os.Stat(sentinel); err == nil && time.Since(fi.ModTime()) < 2*time.Minute {
		return
	}
	if err := os.WriteFile(sentinel, []byte("1"), 0o600); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// Under `go test` the executable is the test binary; spawning it as a
	// refresher would rerun the suite (and hung the Windows runner).
	if strings.HasSuffix(strings.TrimSuffix(exe, ".exe"), ".test") {
		return
	}
	cmd := exec.Command(exe, "hook-refresh")
	cmd.Env = append(os.Environ(), "DEJA_HOOK_REFRESH=1", "CLAUDE_PROJECT_DIR="+cwd)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = cmd.Stdout
	_ = startDetached(cmd)
}

// runHookRefresh recomputes the session-start digest for the cwd in the
// environment and rewrites its cache entry.
func runHookRefresh(dir string) {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// The digest is recomputed from the index, so refreshing it off a stale
	// index only re-serves the same snapshot: a project whose newest session
	// reversed an earlier decision kept handing the agent the earlier one for
	// as long as the user went without running the CLI (#913). This runs
	// detached, off the startup path, so the incremental build costs the hook
	// nothing; a directory that cannot be written stays stale, as before.
	if err := index.Ensure(dir, "", false, nil); err != nil {
		return
	}
	digest, sessions, raw, taskMatched, withheld, ids, projects := hookDigestResult(dir)
	writeHookCache(dir, cwd, digest, sessions, raw, taskMatched, withheld, ids, projects)
	_ = os.Remove(hookCachePath(dir, cwd) + ".refreshing")
}

func hookDigest(dir string) string {
	digest, _, _, _, _, _, _ := hookDigestResult(dir)
	return digest
}

func hookDigestResult(dir string) (string, int, int64, []string, int, []string, []string) {
	return hookDigestResultFor(dir, "")
}

// hookDigestResultFor is hookDigestResult for a caller that was told which
// project the call is about, rather than one reading it back out of the
// environment, where a project deja itself had written stayed for every later
// call in the process (#2182, #2185).
func hookDigestResultFor(dir, fromPayload string) (string, int, int64, []string, int, []string, []string) {
	withheld := 0
	defer func() { _ = recover() }()
	trace := os.Getenv("DEJA_TRACE") == "1"
	t0 := time.Now()
	mark := func(stage string) {
		if trace {
			fmt.Fprintf(os.Stderr, "trace %-16s %6.1fms\n", stage, float64(time.Since(t0).Microseconds())/1000)
			t0 = time.Now()
		}
	}
	_ = mark
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL")))
	if mode == search.RecallOff {
		return "", 0, 0, nil, 0, nil, nil
	}
	// A store from an older index version must be rebuilt before it is read:
	// this path never calls Ensure, so otherwise the first prompts after an
	// upgrade recall nothing and say nothing about why.
	// Damaged as well as stale: a kill during a write leaves records the
	// manifest still describes, and this path answers from them (#800).
	if !index.HasManifest(dir) || !index.IsCurrentVersion(dir) || index.Damaged(dir) {
		requestWarmup(dir)
		return "", 0, 0, nil, 0, nil, nil
	}
	cwd := fromPayload
	if cwd == "" {
		cwd = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", 0, 0, nil, 0, nil, nil
		}
	}
	// The two git probes (worktree list for identity, status/log for the
	// task signal) are independent forks — overlap them.
	taskCh := make(chan []string, 1)
	go func() { taskCh <- changedTaskFiles(cwd) }()
	names := digest.ProjectNameCandidates(cwd)
	mark("names+worktrees")
	pol := policy.Load()
	mark("policy")
	// The standing decisions are scoped by the same trust policy as the session
	// digest: a project the policy withholds from auto-activation must not leak
	// its decisions here either.
	var allowedNames []string
	for _, n := range names {
		if pol.Allows(policy.ActivationAuto, n) {
			allowedNames = append(allowedNames, n)
		}
	}
	conventions := projectConventions(allowedNames, 6, 800)
	var ss []model.Session
	seen := map[string]bool{}
	lookupNames := names
	if mode == search.RecallAggressive {
		recent, err := index.Recent(dir, 12)
		if err == nil {
			lookupNames = nil
			for _, s := range recent {
				lookupNames = append(lookupNames, s.Project)
			}
		}
	}
	// The task signal decides how wide the candidate pool is: with changed
	// files to match against, older sessions are worth considering; without
	// it, recency alone decides and a small pool is enough.
	taskFiles := <-taskCh
	mark("git-taskfiles")
	perName := 3
	if len(taskFiles) > 0 {
		perName = 12
	}
	if got, err := index.RecentProjects(dir, lookupNames, perName); err == nil {
		for _, s := range got {
			if !pol.Allows(policy.ActivationAuto, s.Project) {
				withheld++
				continue
			}
			k := s.Harness + ":" + s.ID
			if seen[k] {
				continue
			}
			seen[k] = true
			ss = append(ss, s)
		}
	}
	mark("load-sessions")
	if len(ss) == 0 {
		// A project can have settled decisions and no recent session left to
		// show them (the transcript was forgotten, or aged past the window).
		// The standing decisions still apply, so serve them alone rather than
		// going out empty.
		if conventions != "" {
			return conventions, 0, 0, nil, withheld, nil, allowedNames
		}
		// withheld travels even with nothing to show: it is the only thing
		// that separates "the rule hid all of it" from "no history here".
		return "", 0, 0, nil, withheld, nil, nil
	}
	scores, matched := taskScores(ss, taskFiles)
	sort.Slice(ss, func(i, j int) bool {
		if scores[ss[i].Harness+":"+ss[i].ID] != scores[ss[j].Harness+":"+ss[j].ID] {
			return scores[ss[i].Harness+":"+ss[i].ID] > scores[ss[j].Harness+":"+ss[j].ID]
		}
		return ss[i].Updated.After(ss[j].Updated)
	})
	if len(ss) > 12 {
		ss = ss[:12]
	}
	// Digest and scoring only ever use the recent tail; hauling a marathon
	// session's megabytes through word sets is pure waste.
	for i := range ss {
		if len(ss[i].Messages) > 150 {
			ss[i].Messages = ss[i].Messages[len(ss[i].Messages)-150:]
		}
	}
	mark("task-scores")
	// A rejected session belongs last, and the mark has to travel with it: the
	// block listed the correction as a separate item and left the session it
	// corrects unmarked (#761).
	ss, rejectedWarning := orderForInjection(ss)
	ss = leadWithUnseen(dir, names, ss)
	result := search.BuildAutoRecall(ss, search.AutoRecallOptions{Mode: mode, ProjectNames: names, TaskScores: scores})
	mark("build-digest")
	if result.Sessions == 0 {
		matched = nil
	}
	text := result.Text
	if rejectedWarning != "" && result.Sessions > 0 {
		text = rejectedWarning + text
	}
	// Inside the cached result, not after it: this costs a manifest scan plus
	// one session read, which is ten times the rest of the hook, and the
	// clusters it reports change over weeks rather than turns.
	var envFrom []string
	if !environmentServedRecently(dir) {
		if env, from := environmentBlockFrom(dir, policy.ActivationAuto); env != "" {
			text += "\n" + env + "\n"
			envFrom = from
			// Stamped on the way out rather than by the check: the check is
			// also made by callers that may not deliver (#1806).
			stampEnvironmentServed(dir)
		}
	}
	mark("environment")
	// The project's standing decisions lead the block: they are the user's own
	// settled choices, and an agent should read them before the session digest,
	// not after. Query-independent, so a convention surfaces even when nothing
	// in the task names it — the gap plain recall cannot close.
	if conventions != "" {
		text = conventions + "\n" + text
	}
	mark("conventions")
	// The candidates that never made it into the digest were never served:
	// counting their transcripts here inflated the distillation ratio deja
	// prints about itself (1 session distilled, 3 sessions' bytes claimed).
	// The projects behind what actually went out, so the injection log can be
	// held to a rule or a forget without reading the digest's prose (#2349).
	return text, result.Sessions, result.RawBytes, matched, withheld, result.IDs, digestProjects(ss, envFrom)
}

// digestProjects names the projects a session-start digest was built from: the
// sessions it shows, plus the ones behind the environment block, which names
// none of them in its own text.
func digestProjects(ss []model.Session, env []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, s := range ss {
		add(s.Project)
	}
	for _, p := range env {
		add(p)
	}
	return out
}

// warmupDeadAfter is how long a warmup may go without publishing progress
// before the next hook treats it as dead and starts another. It is far longer
// than any gap between progress reports — those come at every phase and every
// harness — and far shorter than warmupRetryAfter, which was the whole wait a
// killed build used to cost.
const warmupDeadAfter = 2 * time.Minute

// warmupJustRequested reports that a build was asked for moments ago and has
// not published progress yet. The sentinel carries the time of the request,
// which is exactly what this needs.
func warmupJustRequested(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "warmup.sentinel"))
	if err != nil {
		return false
	}
	stamp, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(0, stamp)) < warmupStatusStale
}

// warmupLooksDead reports whether the build the sentinel stands for has
// stopped without clearing it. A warmup killed mid-build — a closed laptop, an
// OOM, a terminal that took its process group with it — left the sentinel
// behind, and for the next ten minutes every hook returned nothing, spawned
// nothing and said nothing (#875).
func warmupLooksDead(dir string, now time.Time, stamp int64) bool {
	if st := readWarmupStatus(dir); st != nil {
		return false
	}
	return now.Sub(time.Unix(0, stamp)) > warmupDeadAfter
}

func requestWarmup(dir string) {
	if os.Getenv("DEJA_WARMUP_SENTINEL") != "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sentinel := filepath.Join(dir, "warmup.sentinel")
	now := time.Now()
	f, err := os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return
		}
		b, readErr := os.ReadFile(sentinel)
		stamp, parseErr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		// The claim is the O_EXCL create; the stamp lands a moment later. A
		// sentinel read inside that window is empty, and treating empty as
		// unreadable made the second hook delete another hook's claim and
		// spawn a build of its own — two rebuilds over one directory whenever
		// two projects started together (#1065). The file's own mtime says
		// when the claim was made.
		if readErr == nil && parseErr != nil {
			if fi, statErr := os.Stat(sentinel); statErr == nil {
				stamp, parseErr = fi.ModTime().UnixNano(), nil
			}
		}
		if readErr == nil && parseErr == nil && now.Sub(time.Unix(0, stamp)) < warmupRetryAfter && !warmupLooksDead(dir, now, stamp) {
			return
		}
		if os.Remove(sentinel) != nil {
			return
		}
		f, err = os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return
		}
	}
	if _, err := fmt.Fprintln(f, now.UnixNano()); err != nil {
		_ = f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := spawnWarmup(exe, sentinel); err != nil {
		return
	}
}

func startDetachedWarmup(exe, sentinel string) error {
	cmd := exec.Command(exe, "index")
	cmd.Env = append(os.Environ(), "DEJA_WARMUP_SENTINEL="+sentinel)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = cmd.Stdout
	return startDetached(cmd)
}

// limitMarkers are the strings harnesses print when a session dies on quota.
var limitMarkers = []string{
	"usage limit reached",
	"rate limit reached",
	"You've reached your usage limit",
	"usage limit will reset",
	"quota exceeded",
	"out of free quota",
	"upgrade to continue",
	"5-hour limit",
	"weekly limit",
}

// limitHandoffTip checks whether the newest indexed session ended on a usage
// limit and, if so, suggests continuing in a different agent via handoff —
// the cross-agent escape hatch is exactly what limits are for.
func limitHandoffTip(dir string) string {
	recent, err := index.Recent(dir, 1)
	if err != nil || len(recent) == 0 {
		return ""
	}
	s := recent[0]
	// Only a fresh limit matters; an old one is stale advice.
	if s.Updated.IsZero() || time.Since(s.Updated) > 6*time.Hour {
		return ""
	}
	// Recent returns metadata only; the tail scan needs the transcript.
	// Snapshot read ONLY: findByPrefix would run a full synchronous index
	// (10s on a dirty multi-GB store) inside every agent's session start —
	// a garnish line must never cost startup time.
	if full, ok, err := index.FindByPrefix(dir, s.ID); err == nil && ok {
		s = full
	}
	tail := s.Messages
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	for _, m := range tail {
		lower := strings.ToLower(m.Text)
		for _, marker := range limitMarkers {
			if strings.Contains(lower, strings.ToLower(marker)) {
				return fmt.Sprintf("Note: the most recent %s session appears to have hit a usage limit. If that blocks the user again, suggest `deja handoff --to <agent>` — it packages the live context and continues the work in another agent.", s.Harness)
			}
		}
	}
	return ""
}

// serviceReceipt appends today's tally when there is one — the moment memory
// lands is the moment to say what it has been doing all day.
func serviceReceipt(dir string) string {
	recalls, bytes, _ := usage.TodayWithInjections(dir)
	if recalls < 2 || bytes == 0 {
		return ""
	}
	raw := usage.TodayRaw(dir)
	if raw/int64(bytes) < 2 {
		return fmt.Sprintf(" · today: %d recall%s", recalls, pluralS(recalls))
	}
	return fmt.Sprintf(" · today: %d recall%s, %s distilled from %s", recalls, pluralS(recalls), humanBytes(int64(bytes)), humanBytes(raw))
}

// staleReadOnlyNote is the one line for an index that is behind and cannot
// catch up: the store has newer sessions and the directory the rebuild would
// write is not writable. Costs one manifest stat plus one temp-file probe, and
// only when the index is already known to be behind.
func staleReadOnlyNote(dir string) string {
	if indexCanCatchUp(dir) {
		return ""
	}
	return fmt.Sprintf("deja is serving the index as it was — it cannot be updated because %s is not writable", filepath.Dir(dir))
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

// wideRecallLead is sessionStartLead for DEJA_RECALL=aggressive, where the
// sessions come from this machine rather than from this checkout.
const wideRecallLead = "The sessions below are recent work on this machine, not only in this project — deja is set to recall widely. If any is relevant to what the user asks next, call recall_context with a term from it to pull the full details before acting. If recalled history genuinely helps the task, tell the user in one short line what deja-vu recalled and how you reused it; otherwise do not mention it.\n"

const sessionStartLead = "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call recall_context with a term from it to pull the full details before acting. If recalled history genuinely helps the task, tell the user in one short line what deja-vu recalled and how you reused it; otherwise do not mention it.\n"
