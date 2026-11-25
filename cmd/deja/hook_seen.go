package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/hookengine"
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
	return hookengine.SeenKey(s)
}

// hookseenField reports whether a value can be one field of a line as it
// stands. The session id above is mapped because it is only ever compared with
// itself; a value is not, because it is compared with the index's own ids —
// so one that cannot be a field is left out of the file instead.
func hookseenField(s string) bool { return hookengine.IsSeenField(s) }

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
type seenLines hookengine.SeenLines

func hookseenPath(dir string) string {
	return dir + ".hookseen"
}

func openHookseenTx(dir string) (*hookengine.SeenTx, func(), bool) {
	return hookengine.NewLedger(dir).Lock()
}

// holdHookseenLock serializes concurrent hook invocations against the same
// index so two hooks spawned in the same millisecond do not both read an
// un-injected state and emit duplicate blocks (#5930). contended reports
// whether another hook process held the lock when this caller arrived.
func holdHookseenLock(dir string) (func(), bool) {
	_, release, contended := hookengine.NewLedger(dir).Lock()
	return release, contended
}

func loadSeen(dir string) seenLines {
	return seenLines(hookengine.NewLedger(dir).Read())
}

// injected is alreadyInjected over an already-read file.
func (sl seenLines) injected(sid string) map[string]bool {
	return hookengine.SeenLines(sl).Injected(sid)
}

// recent is recentlyInjected over an already-read file.
func (sl seenLines) recent(sid string, window int) map[string]bool {
	return hookengine.SeenLines(sl).Recent(sid, window)
}

// recentInProject is recentlyInjectedInProject over an already-read file.
func (sl seenLines) recentInProject(project string, window int) map[string]bool {
	return hookengine.SeenLines(sl).RecentInProject(project, window)
}

// asked is askedRecently over an already-read file.
func (sl seenLines) asked(sid, key string, window time.Duration) bool {
	return hookengine.SeenLines(sl).Asked(sid, key, window)
}

// blockFingerprint identifies what a block says, so the same words are not
// shown to the same agent session twice while the session they came from stays
// available for what else it holds.
func blockFingerprint(body string) string {
	return hookengine.BlockFingerprint(body)
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
	return hookengine.AskKey(terms)
}

// isSpawnedReader reports whether this recall is for an agent the parent just
// spawned rather than for someone typing. spawnRecall builds the id, and the
// prefix is the only thing that separates the two readers here.
func isSpawnedReader(sid string) bool {
	return strings.HasPrefix(sid, hookengine.SpawnReaderPrefix)
}

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
	hookengine.NewLedger(dir).ForgetSessions(sid)
}

func seenSessionIDs(ss []model.Session) []string {
	ids := make([]string, 0, len(ss))
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	return ids
}

func recordSeenSessionsFor(tx *hookengine.SeenTx, sid, project string, ss []model.Session) {
	tx.RecordForProject(sid, project, seenSessionIDs(ss)...)
}

func rememberInjected(dir, sid string, ss []model.Session) {
	rememberInjectedFor(dir, sid, "", ss)
}

// rememberInjectedFor is rememberInjected with the project the injection went
// to, so recentlyInjectedInProject can count across agent sessions. Callers
// that have no project pass the empty string and write the old three-field
// line.
func rememberInjectedFor(dir, sid, project string, ss []model.Session) {
	rememberInjectedIDsFor(dir, sid, project, seenSessionIDs(ss))
}

// rememberInjectedIDsFor is rememberInjectedFor for a caller that has the ids
// rather than the sessions — the session-start hook, which serves out of a
// cached digest and knows only what went into it.
func rememberInjectedIDsFor(dir, sid, project string, ids []string) {
	hookengine.NewLedger(dir).RecordForProject(sid, project, ids...)
}

// rotateHookseen shrinks the dedup file to this session's lines plus the most
// recent tail, written atomically so a concurrent hook never sees a torn file.
func rotateHookseen(p, sid string) {
	hookengine.RotateFile(p, sid)
}

// rememberInjectedIDs records arbitrary dedupe tokens against a session, so a
// hook that injects something other than a session (a command, a file line)
// can avoid repeating it to the same agent turn after turn.
func rememberInjectedIDs(dir, sid string, ids ...string) {
	hookengine.NewLedger(dir).RecordIDs(sid, ids...)
}
func blockAlreadySentThisSession(dir, sid string) bool {
	if sid == "" {
		return false
	}
	for id, ok := range recentlyInjected(dir, sid, leadWindow) {
		if ok && strings.HasPrefix(id, digestSeenPrefix) {
			return true
		}
	}
	return false
}

// digestsPerSession is how many full per-prompt digests one agent session gets
// before later matches shrink to the pointer line.
const digestsPerSession = 4

// digestSeenPrefix marks a full digest's fingerprint in the seen list, so the
// budget can count them without a row of its own.
const digestSeenPrefix = hookengine.DigestSeenPrefix

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
	return weakRecallPointerAskedIn("", "", "", ss, terms, nil)
}

func weakRecallPointerIn(dir, cwd string, ss []model.Session, terms []string, skip map[string]bool) string {
	return weakRecallPointerAskedIn(dir, cwd, "", ss, terms, skip)
}

func weakRecallPointerAskedIn(dir, cwd, asked string, ss []model.Session, terms []string, skip map[string]bool) string {
	return weakRecallPointerAskedWithNudgeIn(dir, cwd, asked, ss, terms, "", skip)
}

func weakRecallPointerAskedWithNudgeIn(dir, cwd, asked string, ss []model.Session, terms []string, nudge string, skip map[string]bool) string {
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
	out := fmt.Sprintf("deja: this project has history on %q from %s%s — inspect session %#q or call deja in \"recall\" mode with a specific token if it matters here.\n",
		search.SafeLine(topic), when, more, ss[0].ID)
	actionAsked := asked
	if strings.TrimSpace(actionAsked) == "" {
		actionAsked = strings.Join(terms, " ")
	}
	if actionLine := sessionActionMapLineForPrompt(dir, cwd, actionAsked, ss, skip); actionLine != "" && lineAddsNewTargetPath(nudge, actionLine) {
		if compact := compactTargetSupplementLine(nudge, actionLine); compact != "" {
			out += compact + "\n"
		}
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
