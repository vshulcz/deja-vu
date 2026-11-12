package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// hookseenKey makes an agent session id safe to be one field of a `.hookseen`
// line. The file is whitespace-separated and the id arrives in the hook
// payload, which is whatever the host sends. A space cost that session its
// dedup and handed its entries to whatever id its first word named, and a
// newline wrote a line of its own — so a payload could mark memory as already
// shown to any session it liked (#2167).
//
// Writing and reading go through this, so a mapped id matches itself, and every
// id a harness actually sends maps to itself, which is why no existing file
// needs converting.
func hookseenKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
}

// hookseenField reports whether a value can be one field of a line as it
// stands. The session id above is mapped because it is only ever compared with
// itself; a value is not, because it is compared with the index's own ids —
// so one that cannot be a field is left out of the file instead.
func hookseenField(s string) bool { return s != "" && hookseenKey(s) == s }

// alreadyInjected returns the session ids this hook already injected into the
// given agent session, so follow-up prompts do not repeat the same memory.
func alreadyInjected(dir, sid string) map[string]bool {
	return loadSeen(dir).injected(sid)
}

// seenLines is the dedupe file read once. The per-prompt hook asks it four
// questions — what this session has been shown, what it was shown lately, what
// the project was shown lately, and whether this question was already answered
// — and each used to read and split the whole file again. Measured on a
// hermetic stand with a 648 KB list, that was 4 ms of a 24 ms call (#3379).
//
// Empty when the file is missing, which is the same answer every one of those
// questions gave before.
type seenLines []string

func hookseenPath(dir string) string {
	return dir + ".hookseen"
}

func hookseenLockPath(dir string) string {
	return dir + ".hookseen.lock"
}

var hookseenMu sync.Mutex

// holdHookseenLock serializes concurrent hook invocations against the same
// index so two hooks spawned in the same millisecond do not both read an
// un-injected state and emit duplicate blocks (#5930). contended reports
// whether another hook process held the lock when this caller arrived.
func holdHookseenLock(dir string) (func(), bool) {
	if strings.TrimSpace(dir) == "" {
		return func() {}, false
	}
	inProcContended := !hookseenMu.TryLock()
	if inProcContended {
		hookseenMu.Lock()
	}
	path := hookseenLockPath(dir)
	if release, ok, busy := takeInstallLock(path, false); ok || !busy {
		return func() {
			release()
			hookseenMu.Unlock()
		}, inProcContended
	}
	release, _, _ := takeInstallLock(path, true)
	return func() {
		release()
		hookseenMu.Unlock()
	}, true
}

func loadSeen(dir string) seenLines {
	b, err := os.ReadFile(hookseenPath(dir))
	if err != nil {
		return nil
	}
	return seenLines(strings.Split(string(b), "\n"))
}

// injected is alreadyInjected over an already-read file.
func (sl seenLines) injected(sid string) map[string]bool {
	out := map[string]bool{}
	if sid == "" {
		return out
	}
	key := hookseenKey(sid)
	for _, line := range sl {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == key {
			out[parts[1]] = true
		}
	}
	return out
}

// recent is recentlyInjected over an already-read file.
func (sl seenLines) recent(sid string, window int) map[string]bool {
	out := map[string]bool{}
	if sid == "" || window <= 0 {
		return out
	}
	key := hookseenKey(sid)
	kept := 0
	for i := len(sl) - 1; i >= 0 && kept < window; i-- {
		parts := strings.Fields(sl[i])
		if len(parts) < 2 || parts[0] != key {
			continue
		}
		kept++
		out[parts[1]] = true
	}
	return out
}

// recentInProject is recentlyInjectedInProject over an already-read file.
func (sl seenLines) recentInProject(project string, window int) map[string]bool {
	out := map[string]bool{}
	if project == "" || window <= 0 {
		return out
	}
	kept := 0
	for i := len(sl) - 1; i >= 0 && kept < window; i-- {
		parts := strings.Fields(sl[i])
		if len(parts) < 4 || parts[3] != project {
			continue
		}
		kept++
		out[parts[1]] = true
	}
	return out
}

// asked is askedRecently over an already-read file.
func (sl seenLines) asked(sid, key string, window time.Duration) bool {
	if sid == "" || key == "" || window <= 0 {
		return false
	}
	want := hookseenKey(sid)
	cutoff := time.Now().UTC().Add(-window)
	for _, s := range slices.Backward(sl) {
		parts := strings.Fields(s)
		if len(parts) < 3 || parts[0] != want || parts[1] != key {
			continue
		}
		t, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			return false
		}
		return t.After(cutoff)
	}
	return false
}

// blockFingerprint identifies what a block says, so the same words are not
// shown to the same agent session twice while the session they came from stays
// available for what else it holds.
func blockFingerprint(body string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(body), " ")))
	return hex.EncodeToString(sum[:8])
}

// repeatAskWindow is how long one reader's question stays answered. An hour is
// long enough to cover a loop that ticks every minute and short enough that
// coming back to the same subject after real work is answered again — and what
// the reader was shown is still on screen for that stretch, so a second copy
// adds nothing.
const repeatAskWindow = time.Hour

// askKey identifies a question by the terms it searches on rather than by its
// text: the same question typed twice with different filler is the same
// question, and terms are what the ranking sees.
func askKey(terms []string) string {
	sorted := append([]string(nil), terms...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, " ")))
	// The prefix keeps it out of the session-id space this file also holds.
	return "ask:" + hex.EncodeToString(sum[:8])
}

// isSpawnedReader reports whether this recall is for an agent the parent just
// spawned rather than for someone typing. spawnRecall builds the id, and the
// prefix is the only thing that separates the two readers here.
func isSpawnedReader(sid string) bool { return strings.HasPrefix(sid, "task:") }

// injectionCooldown is how many later injections a session sits out after
// being shown. Counted in injections rather than minutes because that is what
// the reader experiences: a run of messages about one thing should not keep
// re-reading the same session, and a day that comes back to it should get it
// again. Ten is the length of a short stretch of work.
const injectionCooldown = 10

// recentlyInjectedInProject is the same cooldown counted across agent sessions
// rather than inside one, because the cooldown above only ever saw the session
// it was called from and every new agent session started blank.
//
// Measured over six weeks of a real log: per-prompt recall made 937 injections
// drawn from 74 distinct sessions, 92% of them repeats, with ten sessions
// carrying 80% of the total — the two commonest being marathons of 2.6M and
// 1.4M words, which match nearly any prompt. The agent is told to say nothing
// about a recall that did not help, so the hundredth serving of the same
// session is correctly met with silence, and `stats --impact` counts that
// silence against us (#2038).
//
// Scoped to the project on purpose: the same session answering the same
// question in a different repository is not the repetition being fixed here.
func recentlyInjectedInProject(dir, project string, window int) map[string]bool {
	return loadSeen(dir).recentInProject(project, window)
}

// recentlyInjected is alreadyInjected narrowed to the last few things this
// agent session was shown.
func recentlyInjected(dir, sid string, window int) map[string]bool {
	return loadSeen(dir).recent(sid, window)
}

// forgetInjected drops one agent session's entries from the seen list, so
// recall may send it again what it was already shown. Used when the harness
// truncates the conversation and the blocks go with it.
func forgetInjected(dir, sid string) {
	// An early return, not a rule: with no id the filter below matches nothing
	// and the file is rewritten unchanged, so this saves a read and a write.
	if sid == "" {
		return
	}
	p := hookseenPath(dir)
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	// Every key this session's memory is filed under, not only the one the
	// per-prompt hook writes: the session-start rows are `start:<sid>` and the
	// once mark is `once:<sid>`, so a compaction dropped the prompt rows and
	// kept the two that say the digest has already been sent — leaving the
	// next start refusing to send back exactly what the compaction lost. On a
	// harness whose only rows are those two, nothing was forgotten at all
	// (#3307).
	key := hookseenKey(sid)
	keys := map[string]bool{key: true}
	// Only for an id that is not itself one of those keys. No harness writes a
	// session id beginning with a prefix deja owns, and if one ever did, the
	// prefixed forms would be another session's rows rather than this one's —
	// so the narrow, exact behaviour is what that case gets.
	if !hookseenPrefixed(key) {
		keys[hookseenKey(sessionStartKeyPrefix+sid)] = true
		keys[hookseenKey(onceDigestKey(sid))] = true
	}
	var kept []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line == "" {
			continue
		}
		// A packet's delivery mark stays. It names one capture, and the next
		// compaction is a new capture with a new mark, so keeping it never holds
		// a packet back. Dropping it did: a forked agent that compacts runs
		// PreCompact under its parent's session id, and the parent's packet from
		// a day earlier was delivered again on its next prompt.
		if parts := strings.Fields(line); len(parts) >= 2 && keys[parts[0]] && !strings.HasPrefix(parts[1], "compaction:") {
			continue
		}
		kept = append(kept, line)
	}
	out := ""
	if len(kept) > 0 {
		out = strings.Join(kept, "\n") + "\n"
	}
	_ = atomicfile.Write(p, []byte(out), 0o600)
}

func rememberInjected(dir, sid string, ss []model.Session) {
	rememberInjectedFor(dir, sid, "", ss)
}

// rememberInjectedFor is rememberInjected with the project the injection went
// to, so recentlyInjectedInProject can count across agent sessions. Callers
// that have no project pass the empty string and write the old three-field
// line.
func rememberInjectedFor(dir, sid, project string, ss []model.Session) {
	ids := make([]string, 0, len(ss))
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	rememberInjectedIDsFor(dir, sid, project, ids)
}

// rememberInjectedIDsFor is rememberInjectedFor for a caller that has the ids
// rather than the sessions — the session-start hook, which serves out of a
// cached digest and knows only what went into it.
func rememberInjectedIDsFor(dir, sid, project string, ids []string) {
	if sid == "" {
		return
	}
	p := hookseenPath(dir)
	// Past 1MB the dedup file used to stop writing entirely, which permanently
	// broke dedup — the per-prompt hook then re-injected the same sessions turn
	// after turn. Rotate instead: keep this session's own lines (so its dedup
	// survives) plus the recent tail, then append. Rare (once per 1MB) and
	// atomic, so concurrent hooks lose a few advisory entries at worst.
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		rotateHookseen(p, sid)
	}
	// O_RDWR so the append can read the last byte first: a hook killed before
	// its newline leaves half a line, and writing onto it costs the record
	// written after — a session deja then does not know it has shown, which is
	// this file's whole job (#1967).
	f, err := atomicfile.OpenSharedFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if atomicfile.EndsMidLine(f) {
		_, _ = f.WriteString("\n")
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	for _, id := range ids {
		// The value stays byte-exact: it is looked up against the index's own
		// ids, and a mapped one would match nothing there. One that cannot be
		// a field is dropped instead — a missing dedup entry costs a second
		// showing, where a broken line costs the entry written after it.
		if !hookseenField(id) {
			continue
		}
		if project == "" {
			fmt.Fprintf(f, "%s %s %s\n", hookseenKey(sid), id, stamp)
			continue
		}
		// The project has no spaces by the time it gets here — it is one
		// candidate name, not a path — so the line stays field-separated.
		fmt.Fprintf(f, "%s %s %s %s\n", hookseenKey(sid), id, stamp, project)
	}
}

// rotateHookseen shrinks the dedup file to this session's lines plus the most
// recent tail, written atomically so a concurrent hook never sees a torn file.
func rotateHookseen(p, sid string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	// The tail budget is well under the 1MB cap so rotation is infrequent.
	const tailLines = 4000
	start := 0
	if len(lines) > tailLines {
		start = len(lines) - tailLines
	}
	// The caller's own lines are kept beyond the tail so its dedup survives the
	// rotation — but only as many as the tail itself. Keeping all of them left
	// a long session able to fill the file with its own entries, after which
	// every write rotated a megabyte again: the tool hooks write a token per
	// call, so that is reachable rather than theoretical (#2164).
	var keep, mine, packets []string
	prefix := hookseenKey(sid) + " "
	for i, ln := range lines {
		if ln == "" {
			continue
		}
		switch {
		case i >= start:
			keep = append(keep, ln)
		case strings.HasPrefix(ln, prefix):
			mine = append(mine, ln)
		case compactionMark(ln):
			packets = append(packets, ln)
		}
	}
	if len(mine) > tailLines {
		mine = mine[len(mine)-tailLines:]
	}
	if len(packets) > compactionMarksKept {
		packets = packets[len(packets)-compactionMarksKept:]
	}
	_ = atomicfile.Write(p, []byte(strings.Join(append(append(packets, mine...), keep...), "\n")+"\n"), 0o600)
}

// compactionMarksKept bounds the compaction marks a rotation carries over. One
// is written per compaction, so this is hundreds of compactions' worth.
const compactionMarksKept = 1000

// compactionMark reports a line that says a compaction packet was delivered,
// or must not be. Those are once-only, and a long session's packet can be
// delivered days before the rotation comes: losing the mark then delivers it
// again into a session that has moved on.
func compactionMark(line string) bool {
	parts := strings.Fields(line)
	return len(parts) >= 2 && strings.HasPrefix(parts[1], "compaction")
}

// rememberInjectedIDs records arbitrary dedupe tokens against a session, so a
// hook that injects something other than a session (a command, a file line)
// can avoid repeating it to the same agent turn after turn.
func rememberInjectedIDs(dir, sid string, ids ...string) {
	if sid == "" {
		return
	}
	p := hookseenPath(dir)
	// Rotate rather than stop, for the reason rememberInjected does: a full
	// file used to end the dedup for good, and the hooks that write tokens are
	// the ones whose whole job is not repeating themselves to the same agent.
	// Stopping here left that to whenever a session injection happened to
	// rotate the file, and on a machine whose hooks inject tokens rather than
	// sessions, nothing ever did (#2164).
	if fi, err := os.Stat(p); err == nil && fi.Size() > 1<<20 {
		rotateHookseen(p, sid)
	}
	f, err := atomicfile.OpenSharedFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if atomicfile.EndsMidLine(f) {
		_, _ = f.WriteString("\n")
	}
	for _, id := range ids {
		if !hookseenField(id) {
			continue
		}
		fmt.Fprintf(f, "%s %s\n", hookseenKey(sid), id)
	}
}
func blockAlreadySentThisSession(dir, sid string) bool {
	if sid == "" {
		return false
	}
	return len(recentlyInjected(dir, sid, leadWindow)) > 0
}

// digestsPerSession is how many full per-prompt digests one agent session gets
// before later matches shrink to the pointer line.
const digestsPerSession = 4

// digestSeenPrefix marks a full digest's fingerprint in the seen list, so the
// budget can count them without a row of its own.
const digestSeenPrefix = "digest:"

// digestBudgetSpent reports whether this session has had its full digests. A
// spawned agent has no count of its own: it gets the pointer unless the
// question was asked before, which the caller decides.
func digestBudgetSpent(seen map[string]bool, sid string) bool {
	if sid == "" || strings.HasPrefix(sid, spawnReaderPrefix) {
		return false
	}
	n := 0
	for id := range seen {
		if strings.HasPrefix(id, digestSeenPrefix) {
			n++
		}
	}
	return n >= digestsPerSession
}

// leadWindow is how far back the check reads. Longer than the injection
// cooldown on purpose: this asks whether the session was ever told, not whether
// a particular memory is on cooldown.
const leadWindow = 500

// digestBudget is how much room the block gets. A match resting on a single
// rare word is a weaker claim than one resting on two, and it is where most of
// the injections on unrelated prompts come from: measured on cross-paired
// LongMemEval prompts, halving that case takes the average injected block from
// 1077 to 977 characters. It costs nothing that can be measured on real
// questions — 52 answers of 58 either way — because the block that answers a
// single-word question rarely needed the second half.
func digestBudget(confident bool) int {
	budget := promptHookBudget - recallFrameOverhead
	if !confident {
		budget /= 2
	}
	return budget
}

// weakRecallPointer is what an unconfident match injects instead of a digest:
// one line saying history exists and how to reach it. A single rare term is a
// hint, not an answer, and this hook runs on every message — spending a full
// digest on a hint is where most of deja's unprompted context cost went.
// Naming the topic and the date is what makes the pointer worth following.
func weakRecallPointer(ss []model.Session, terms []string) string {
	return weakRecallPointerIn("", "", ss, terms, nil)
}

func weakRecallPointerIn(dir, cwd string, ss []model.Session, terms []string, skip map[string]bool) string {
	if len(ss) == 0 {
		return ""
	}
	s := ss[0]
	// With the terms, which is the whole point of the line: the pointer's only
	// content is the topic it names, because the agent has to turn that into a
	// recall call. Called without them, dejaVuTopic falls through to the
	// session's title — and on the one firing measured against a real store
	// that answered "how is the block" with `"делай задачи в todo.md"`, a title
	// from a session that merely mentions the subject somewhere. The spoken
	// déjà vu line has passed its terms since it was written (#2734); this
	// path did not, so the cheaper payload was also the wronger one.
	topic := dejaVuTopic(s, terms...)
	if topic == "" {
		topic = strings.Join(terms, " ")
	}
	// Bounded the way the spoken opener bounds the same kind of line. The
	// matched line is a whole user turn, which on this store reaches a thousand
	// characters — and the pointer exists because it is cheap: 290 bytes became
	// 1234 with the line unclipped, which is a digest's price for a pointer's
	// content.
	topic = clipTopic(topic)
	when := "earlier"
	if !s.Updated.IsZero() {
		when = search.RelativeDate(s.Updated)
	}
	more := ""
	if len(ss) > 1 {
		more = fmt.Sprintf(" (+%d more)", len(ss)-1)
	}
	out := fmt.Sprintf("deja: this project has history on %q from %s%s — call deja in \"recall\" mode (or recall) with a specific token if it matters here.\n",
		search.SafeLine(topic), when, more)
	if actionLine := sessionActionMapLineForPrompt(dir, cwd, strings.Join(terms, " "), ss, skip); actionLine != "" {
		out += actionLine + "\n"
	}
	return out
}

// pointerTopicRunes is how much of the matched line the pointer names. Sixty is
// what the spoken opener uses for the same job, and the agent only needs enough
// to write a recall call.
const pointerTopicRunes = 60

// clipTopic bounds a topic to one readable clause, ending at a word where it
// can. Cutting mid-word leaves a fragment nobody can search for, which is the
// one thing this line is for.
func clipTopic(topic string) string {
	topic = strings.Join(strings.Fields(topic), " ")
	r := []rune(topic)
	if len(r) <= pointerTopicRunes {
		return topic
	}
	cut := string(r[:pointerTopicRunes])
	// Back up to the last space when one is near, so the reader gets whole
	// words; a long unbroken token keeps the hard cut.
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)-20 && i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:—-") + "…"
}

// hookseenPrefixed reports whether a key already carries one of the prefixes
// deja writes its own rows under, so a session id shaped like one of them does
// not reach across into another session's rows.
func hookseenPrefixed(key string) bool {
	for _, p := range []string{sessionStartKeyPrefix, "once:", "agy:"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
