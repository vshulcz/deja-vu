package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// dejaVuLineDue rate-limits the visible line: a déjà vu that fires every
// prompt is wallpaper, and wallpaper trains the user to ignore the real
// moments. Context still flows to the agent regardless.
//
// The limit is per session, not per machine. Keyed on the index alone it also
// silenced sessions that had never shown a line at all: on one index, four
// fresh sessions inside twenty minutes received recall and one of them said
// so. The people deja is for run several agents at once, so that was the
// common case rather than the corner. A session with no id — a host that
// sends none — falls back to the machine-wide window, which is the old
// behaviour and still better than talking on every prompt.
func dejaVuLineDue(dir, sid string) bool {
	p := dir + ".dejavu"
	now := time.Now()
	key := dejaVuKey(sid)
	if b, err := os.ReadFile(p); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != key {
				continue
			}
			if ts, err := strconv.ParseInt(fields[1], 10, 64); err == nil &&
				now.Sub(time.Unix(ts, 0)) < dejaVuLineWindow {
				return false
			}
		}
	}
	return recordDejaVuLine(p, key, now)
}

// dejaVuKey is how a session is named in `.dejavu`, which is two fields to a
// line and holds the machine-wide fallback under "-".
//
// Mapped, for the reason hookseenKey is: the id comes from the hook payload, a
// space made the line three fields so nothing ever matched it — the notice then
// fired on every prompt, which the limit exists to prevent — and a newline
// wrote a line under whatever followed it, spending another session's window.
// Prefixed, so an agent calling itself "-" cannot share the fallback's window
// with a host that sent no id at all. Entries age out inside twenty minutes, so
// the shape can change without converting anything (#2170).
func dejaVuKey(sid string) string {
	if sid == "" {
		return "-"
	}
	return "s" + hookseenKey(sid)
}

const (
	dejaVuLineWindow = 20 * time.Minute
	// dejaVuLineKeep bounds the file: one line per session that saw a notice
	// recently, and an agent fleet is tens rather than thousands. Entries
	// older than the window are dropped on every write, so this only caps a
	// burst.
	dejaVuLineKeep = 64
)

// recordDejaVuLine stamps this session and rewrites the file without the
// entries that have aged out. It reports true so callers can return it
// directly: failing to write is not a reason to withhold the line.
func recordDejaVuLine(path, sid string, now time.Time) bool {
	kept := []string{sid + " " + strconv.FormatInt(now.Unix(), 10)}
	if b, err := os.ReadFile(path); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] == sid {
				continue
			}
			ts, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || now.Sub(time.Unix(ts, 0)) >= dejaVuLineWindow {
				continue
			}
			if len(kept) >= dejaVuLineKeep {
				break
			}
			kept = append(kept, line)
		}
	}
	_ = atomicfile.Write(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
	return true
}

// viaTerms picks the three query terms the headline will name. Terms the block
// underneath actually carries come first: the line is the reason given for the
// recall, and naming words the reader cannot find below it reads as a misfire.
// Measured on a real store, the block opened on a line carrying a term the
// product used in 94 cases of 94, while the first three terms in extraction
// order agreed with it in 71.
func viaTerms(block string, terms []string) []string {
	if len(terms) <= 3 {
		return terms
	}
	out := make([]string, 0, 3)
	for _, t := range terms {
		if len(out) == 3 {
			break
		}
		if search.TextCarriesTerm(block, t) {
			out = append(out, t)
		}
	}
	// Short of three, fill from the rest in the order they were asked, so the
	// line still says what the question was about.
	for _, t := range terms {
		if len(out) == 3 {
			break
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// dejaVuLine is the one visible line a déjà vu moment earns: which agent the
// memory came from, how old it is, and what was said there.
func dejaVuLine(s model.Session, terms ...string) string {
	topic := dejaVuTopic(s, terms...)
	if topic == "" {
		return ""
	}
	// Callers pass terms already chosen by viaTerms; the cap stays here so
	// a caller that has not is still held to three.
	if len(terms) > 3 {
		terms = terms[:3]
	}
	lead := fmt.Sprintf("deja-vu — %s: ", dejaVuLead(s))
	// The quote, measured as printed (quotes and escapes included), gets what
	// the line has left after the lead and room for one term of up to 16
	// columns: at most 50 columns, and 26 unless a long agent name on an
	// imported session leaves less.
	room := noteWidth - termwidth.Columns(lead)
	floor := min(26, room)
	if len(terms) > 0 {
		longest := 0
		for _, t := range terms {
			longest = max(longest, termwidth.Columns(t))
		}
		room -= termwidth.Columns(" (via )") + min(longest, 16)
	}
	quoted := quoteWithin(topic, max(floor, min(50, room)))
	// A term the quote already shows tells the reader nothing the quote does
	// not, so the ones it lacks go first and are the last to be dropped. The
	// quote as cut is what the reader sees, so that is what is checked.
	shown := topic
	if u, err := strconv.Unquote(quoted); err == nil {
		shown = u
	}
	lower := strings.ToLower(shown)
	terms = slices.Clone(terms)
	slices.SortStableFunc(terms, func(a, b string) int {
		ina, inb := strings.Contains(lower, strings.ToLower(a)), strings.Contains(lower, strings.ToLower(b))
		switch {
		case ina == inb:
			return 0
		case inb:
			return -1
		}
		return 1
	})
	// No colon after the name, for the reason the session-start receipts lost
	// theirs: the host introduces the line itself. Claude Code renders this as
	// "UserPromptSubmit says: …", which read "says: deja-vu: you have been
	// here" — seen on screen, two colons in four words.
	//
	// Name the terms that triggered the moment: a line with no visible reason
	// reads as noise the first time it misfires. The first always, cut if it
	// alone would pass noteWidth, the rest while they fit.
	head := lead + quoted
	why := ""
	for i, t := range terms {
		if i == 0 {
			left := noteWidth - termwidth.Columns(head+" (via )")
			if termwidth.Columns(t) > left {
				if left < 4 {
					break
				}
				t = termwidth.Cut(t, left-1) + "…"
			}
			why = " (via " + t
			continue
		}
		next := why + ", " + t
		if termwidth.Columns(head+next+")") > noteWidth {
			break
		}
		why = next
	}
	if why == "" {
		return head
	}
	return head + why + ")"
}

// dejaVuLead says where the memory came from and when: "from Codex CLI, 4
// months ago". The agent is the point of the line: a question asked in Claude
// Code that Codex settled in the spring is what no single agent's memory can
// answer, and "you have been here" left that out.
func dejaVuLead(s model.Session) string {
	var from string
	switch s.Harness {
	case "":
	case "deja":
		from = "from your notes"
	default:
		from = "from " + agentName(s.Harness)
	}
	// A session that arrived from another machine is not work the reader did
	// here: the context block below labels it `imported:` and the line has to
	// agree with it (#1001).
	if s.IsImported() {
		if from == "" {
			from = "from another machine"
		} else {
			from += " on another machine"
		}
	}
	age := dejaVuAge(s.Updated)
	switch {
	case from != "" && age != "":
		return from + ", " + age
	case from != "":
		return from
	case age != "":
		return age
	}
	return "you have been here"
}

// dejaVuAge is how long ago, in the words a person uses: "today", "3d ago",
// "2 weeks ago", "4 months ago". A calendar date leaves the reader to do the
// subtraction, and the distance is what makes the moment worth a line.
func dejaVuAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	rel := search.RelativeDate(t)
	days := int(time.Since(t).Hours() / 24)
	switch {
	case rel == "today" || strings.HasSuffix(rel, " ago") || days < 7:
		// Ahead of the clock, or on the boundary of a week: the date is what is known.
		return rel
	case days < 60:
		return countNoun(days/7, "week") + " ago"
	case days < 365:
		return countNoun(days/30, "month") + " ago"
	}
	return countNoun(days/365, "year") + " ago"
}

// dejaVuTopic picks something a human actually typed. Session titles are the
// first user message, which for some harnesses is injected plumbing
// ("# AGENTS.md instructions <INSTRUCTIONS>...") — showing that as "you have
// been here" reads as a glitch, not a memory.
func dejaVuTopic(s model.Session, terms ...string) string {
	// What the recall actually matched, before the session's title or its
	// opening line. This is the one line a person reads, and on a long session
	// narrowed to its matching region the opening line is whatever chatter
	// began that window: seen on screen as "you have been here — \"migration
	// locked the table\"" answering a question about token rotation.
	if t := strings.TrimSpace(search.MatchedUserLine(s, terms)); t != "" && presentableTopic(t) {
		return t
	}
	if t := strings.TrimSpace(s.Title); t != "" && presentableTopic(t) {
		return t
	}
	for _, m := range s.Messages {
		if m.Role != "user" || digest.IsAgentArtifact(m.Text) {
			continue
		}
		t := strings.TrimSpace(digest.MessageText(m.Text))
		if t != "" && presentableTopic(t) {
			return t
		}
	}
	return ""
}

func presentableTopic(t string) bool {
	if digest.IsAgentArtifact(t) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(t)
	if r == '#' || r == '<' || r == '{' || r == '[' {
		return false
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r >= 0x400
}

// rankedAlreadyShown counts candidates the ranking returned despite having been
// injected already. A seam, like focusCalls below: such a session is read from
// disk in full and then dropped on its id, and nothing in the output changes
// either way, so only a count can hold the skip in place.
var rankedAlreadyShown int

// focusCalls counts how many sessions were narrowed. A seam, like
// rebuildInProgress above: narrowing walks every message of a session that can
// run to sixteen thousand of them, and the two cheap reasons to discard a
// candidate are checked before it. Nothing else can hold that order in place —
// the output is identical either way and only the time differs, and a test that
// watches a clock is a test that fails on someone else's machine.
var focusCalls int

// focusSession narrows a long session to the neighbourhood of the messages
// that matched, so a haystack contributes its relevant part rather than being
// dropped or flooding the digest. Two messages either side keeps the exchange
// readable: a question without its answer recalls nothing useful.
func focusSession(s model.Session, terms []string) model.Session {
	focusCalls++
	const window = 2
	keep := map[int]bool{}
	// Lowercase every message once and hand the result to both passes: the
	// narrowing below and, when the session stays too big, the density pass
	// after it. Each of them used to lowercase the whole session again, and a
	// marathon session is thousands of messages.
	//
	// The same rule the block is built with. This step spelled the word the
	// way the question happened to spell it, while the ranking that chose the
	// session and the digest that shows a line from it both fold the ending —
	// so a session whose only mention was in another case was narrowed to
	// nothing and dropped whole.
	//
	// Asked for the whole query at once: per term, each call lowercased the
	// entire message again. Spread over the cores, since each message is
	// independent and this pass was a third of the per-prompt hook's wall time
	// on a store with marathon sessions.
	low := make([]string, len(s.Messages))
	hit := make([]bool, len(s.Messages))
	parallelChunks(len(s.Messages), func(i int) {
		low[i] = strings.ToLower(s.Messages[i].Text)
		hit[i] = search.TermHitsLowered(low[i], terms) > 0
	})
	for i := range s.Messages {
		if !hit[i] {
			continue
		}
		for j := i - window; j <= i+window; j++ {
			if j >= 0 && j < len(s.Messages) {
				keep[j] = true
			}
		}
	}
	if len(keep) == 0 {
		s.Messages = nil
		return s
	}
	focused := make([]model.Message, 0, len(keep))
	for i, m := range s.Messages {
		if keep[i] {
			focused = append(focused, m)
		}
	}
	// A term common enough to appear throughout keeps most of the session,
	// which is the haystack again. Rather than give up, keep the densest
	// windows — the places where the most distinct terms land together are
	// where the answer is, and the rest is the session's background noise.
	if len(focused) > dejaVuMaxMessages {
		s.Messages = densestMessages(s.Messages, low, terms, dejaVuMaxMessages)
		return s
	}
	s.Messages = focused
	return s
}

// parallelChunks runs fn over 0..n-1 in chunks handed out to one goroutine per
// core. fn must write only to its own index.
func parallelChunks(n int, fn func(i int)) {
	const chunk = 64
	workers := min(runtime.GOMAXPROCS(0), (n+chunk-1)/chunk)
	if workers < 2 {
		for i := range n {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				lo := int(next.Add(chunk)) - chunk
				if lo >= n {
					return
				}
				for i := lo; i < min(lo+chunk, n); i++ {
					fn(i)
				}
			}
		}()
	}
	wg.Wait()
}

// densestMessages keeps the cap best messages by how many distinct terms each
// one carries, in original order so the exchange still reads as a conversation.
func densestMessages(msgs []model.Message, low, terms []string, limit int) []model.Message {
	type scored struct {
		i, hits int
	}
	hitsAt := make([]int, len(msgs))
	parallelChunks(len(msgs), func(i int) {
		for _, t := range terms {
			if strings.Contains(low[i], t) {
				hitsAt[i]++
			}
		}
	})
	var ranked []scored
	for i, hits := range hitsAt {
		if hits > 0 {
			ranked = append(ranked, scored{i, hits})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].hits > ranked[b].hits })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	keep := make(map[int]bool, len(ranked))
	for _, r := range ranked {
		keep[r.i] = true
	}
	out := make([]model.Message, 0, len(keep))
	for i, m := range msgs {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

// sessionProjects names the projects behind a served digest, deduped in order.
// The digest log records them so a rule tightened later can be applied to the
// stored text without the sessions still being in the index (#2324).
func sessionProjects(ss []model.Session) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.Project == "" || seen[s.Project] {
			continue
		}
		seen[s.Project] = true
		out = append(out, s.Project)
	}
	return out
}

// sessionIDs is what the usage log needs to attribute a déjà vu moment to the
// sessions it was about.
func sessionIDs(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.ID != "" {
			out = append(out, s.ID)
		}
	}
	return out
}

// promptHookLead says what the block is before it says what to do with it.
// "deja found prior sessions matching this request" reads as a judgement that
// they answer it, and an agent acted on that: asked about one service with
// another's symptom in the prompt, it applied the neighbouring session's
// decision and said history had settled it — naming the mismatch in the same
// sentence (#2370). The match is on wording, and the lead now says so and asks
// for the one check that catches it.
const promptHookLead = "deja found sessions whose wording matches this request — not a judgement that they answer it. Check that the session describes what is happening now before acting on it. If one genuinely helps, answer the user from it, and credit it in the one line the end of this block asks for; otherwise ignore silently.\n"

// The short lead that blocks after the first used to carry — "deja matched on
// wording, not meaning — check the session fits before acting on it." — is
// gone. Measured on the local 9B over 40 cases where the recalled history is
// about a sibling subject and holds a value nothing else does: quoting it as
// the answer, unhedged, happened 12 times of 40 with that line and 10 of 40
// without it. The care it asked for is not care the line produces.

// blockAlreadySentThisSession reports whether this agent session has had a
// per-prompt block before. The seen list is written after every block that goes
// out, keyed by the session, so its own rows are the record of that.
