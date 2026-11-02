package search

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

func mergeSessions(in []model.Session) []model.Session {
	by := map[string]*model.Session{}
	var order []string
	for _, s := range in {
		k := s.Harness + ":" + s.ID
		if by[k] == nil {
			cp := s
			by[k] = &cp
			order = append(order, k)
		} else {
			by[k].Messages = append(by[k].Messages, s.Messages...)
			by[k].Touch(s.Updated)
			if by[k].Project == "history" {
				by[k].Project = s.Project
			}
		}
	}
	// In first-seen order, so equal scores further on do not tie-break on map
	// iteration.
	out := make([]model.Session, 0, len(by))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

// jsonHitMessages is how many messages one hit may carry. Three excerpts is
// what the text path shows; twenty leaves room for the passages around them
// and still bounds an answer a script has to hold in memory.
const jsonHitMessages = 20

// boundedHit is one hit with its session's message list reduced to the
// passages that matched.
//
// The whole list was encoded before: 136 MB for one relevance answer on a real
// store, a second of the 2.6 it took, and half a gigabyte of resident memory —
// all of it text the caller did not ask about, since a hit's own excerpts are
// in `snippets` and a whole session is what `show --json` is for (#3620).
//
// A tier that records no indices — nothing in tree does, but the field is
// unexported and a future one might not — keeps the newest messages, where a
// session says what it settled.
func boundedHit(h Hit) Hit {
	var total int
	h.Session.Messages, total = boundedSelection(h.Session, h.matched)
	if total > 0 {
		h.MessagesTotal, h.MessagesCapped = total, true
	}
	return h
}

// boundedSelection is a session's messages bounded to the matched passages,
// and the session's whole count when what is kept is a selection of it; zero
// when the whole session fits.
func boundedSelection(s model.Session, matched []int) ([]model.Message, int) {
	if len(s.Messages) == 0 {
		return s.Messages, 0
	}
	whole := max(len(s.Messages), s.Held)
	kept, _ := boundedMessages(s.Messages, matched)
	if len(kept) < whole {
		return kept, whole
	}
	return kept, 0
}

// boundedMessages keeps the messages at the given indices, in message order,
// at most jsonHitMessages of them.
//
// No indices — nothing in tree, but the field is unexported and a future tier
// might not set it — keeps the newest, where a session says what it settled.
func boundedMessages(ms []model.Message, matched []int) ([]model.Message, bool) {
	total := len(ms)
	keep := make([]int, 0, len(matched)*2)
	for _, i := range matched {
		// The match and the line after it: a question is answered by what
		// follows, which is why the MCP listing attaches exactly that (#1163).
		keep = append(keep, i, i+1)
	}
	if len(matched) == 0 {
		from := max(total-jsonHitMessages, 0)
		for i := from; i < total; i++ {
			keep = append(keep, i)
		}
	}
	// Without repeats: a message can match several terms, and the exact path
	// appends one candidate per match.
	seen := make(map[int]bool, len(keep))
	order := make([]int, 0, len(keep))
	for _, i := range keep {
		if i < 0 || i >= total || seen[i] {
			continue
		}
		seen[i] = true
		order = append(order, i)
	}
	sort.Ints(order)
	if len(order) > jsonHitMessages {
		// The newest of them: a long session's later word on the subject is
		// the one worth carrying, which is the rule the third excerpt follows.
		order = order[len(order)-jsonHitMessages:]
	}
	if len(order) == total {
		return ms, false
	}
	out := make([]model.Message, 0, len(order))
	for _, i := range order {
		out = append(out, ms[i])
	}
	return out, true
}

// SafeSession is a session with the text a transcript supplied filtered the way
// the printer filters it: newlines and tabs survive, because a JSON string
// holds them and a reader may want the shape of a message, and the characters
// that make displayed order disagree with stored order do not.
//
// The JSON surfaces need this and the printed ones do not: a control byte is
// escaped by the encoder, while U+202E and the invisible tag block are ordinary
// characters to it, so a reader rendering the text got the reordering and the
// invisible instructions the text path strips (#1090, #3616).
func SafeSession(s model.Session) model.Session {
	s.Title = SafeText(s.Title)
	s.Project = SafeText(s.Project)
	if len(s.Messages) > 0 {
		ms := make([]model.Message, len(s.Messages))
		copy(ms, s.Messages)
		for i := range ms {
			ms[i].Text = SafeText(ms[i].Text)
		}
		s.Messages = ms
	}
	return s
}

// SafeStrings is SafeSession for the free-text lists beside a session: the
// snippets a hit quotes, the titles blame carries.
func SafeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = SafeText(s)
	}
	return out
}

func safeHit(h Hit) Hit {
	h.Session = SafeSession(h.Session)
	h.Snippets = SafeStrings(h.Snippets)
	h.LifecycleNote = SafeText(h.LifecycleNote)
	return h
}

// Print writes search hits as text, or as the JSON envelope with o.JSON.
func Print(w io.Writer, hits []Hit, o Options) {
	for i := range hits {
		if hits[i].Tier == "" {
			hits[i].Tier = TierExact
		}
		hits[i].Session.SetSource(o.SourceInstance)
	}
	if o.JSON {
		// The same filter the printer applies. A control byte is escaped by the
		// encoder, but U+202E and the invisible tag block are ordinary
		// characters to it, so a reader rendering the text got the reordering
		// and the invisible instructions that SafeText exists to stop — on the
		// one surface a dashboard reads (#3616).
		for i := range hits {
			hits[i] = safeHit(boundedHit(hits[i]))
		}
		// One shape, always. The exact path used to emit a bare array while
		// every fallback path emitted an object, so a consumer had to handle
		// two contracts and could not tell which it had until it looked.
		_ = json.NewEncoder(w).Encode(searchJSONEnvelope{ //nolint:errchkjson // no error return here; write errors are ignored like the text path
			SchemaVersion:  jsonout.Version,
			Tier:           setTier(o),
			Total:          o.Total,
			TotalEstimated: o.TotalEstimated,
			Strict:         o.Strict,
			Capped:         o.Capped,
			Withheld:       o.PolicyWithheld,
			Hits:           hits,
			Fuzzy:          o.Fuzzy,
			Stemmed:        o.Stemmed,
			Semantic:       o.Semantic,
			Variants:       o.FuzzyVariants,
		})
		return
	}
	color := colorOK(w)
	// One form for the whole column, decided before the first row is printed.
	when := make([]time.Time, 0, len(hits))
	for _, h := range hits {
		t := h.Session.Updated
		if day, ok := NoteBucketDay(h.Session); ok {
			t = noteBucketNoon(day)
		}
		when = append(when, t)
	}
	dated := dateColumn(when)
	for i, h := range hits {
		d := "-"
		if !when[i].IsZero() {
			// A day of notes is one session whose id *is* a date, minted in
			// UTC. The reader's zone put a different day on the line than the
			// id beside it, and only for deja's own buckets (#883) — which is
			// why the time was chosen above rather than here.
			d = dated(when[i])
		}
		// project and id are transcript/peer free text printed to a terminal;
		// an escape or bidi run in an imported project name would repaint the
		// line the way it would in blame. Snippets below are already SafeText'd.
		project := SafeLine(h.Session.Project)
		id := SafeLine(short(h.Session.ID))
		// The project column gives way first. Cutting the line at its end takes
		// the match count and half the word "matches" with it, and those are
		// what a reader scans; the project is the one field that is long
		// because a directory was named at length (#604).
		project = fitProject(project, o.Width, h.Session.Harness, d, id, h.Count, tierLabel(h))
		if color {
			fmt.Fprintf(w, "%s%s %-10s %s %s %s %s %s%s%s%s%s\n", cBold, harnessTag(h.Session.Harness, true), project, cDim+"·"+cReset+cBold, d, cDim+"·"+cReset+cBold, id, cDim+"— "+cReset, cBold, MatchCount(h.Count), cReset, tierLabel(h))
		} else {
			fmt.Fprintf(w, "[%s] %-10s · %s · %s — %s%s\n", h.Session.Harness, project, d, id, MatchCount(h.Count), tierLabel(h))
		}
		if h.Reused > 1 {
			note := fmt.Sprintf("  reused %d× by agents recently", h.Reused)
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		// What was later recorded about this decision comes before anything
		// else about the hit: a reader who stops after one line must not stop
		// on a conclusion that was reverted.
		if h.Lifecycle != "" {
			note := "  " + lifecycleSummary(h)
			if color {
				note = cOrange + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		// Nobody sets the rejected state by hand, so the sessions that ended in
		// a dead end look exactly like the ones that ended in an answer. When
		// the transcript itself says something was backed out, say so — as
		// evidence from the session, not as a state someone recorded. The
		// wording is deliberately mild: a session that tried one thing, dropped
		// it and found another is the most useful kind, so this flags an
		// abandoned approach inside it, not the whole session as a dead end.
		if h.Session.GaveUp && h.Lifecycle == "" && AbandonmentWorthSaying(h) {
			note := "  mentions backing an approach out — one path here was abandoned"
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		if h.Moved != "" {
			note := SafeLine(h.Moved)
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		if h.Superseded != "" {
			note := "  earlier attempt — this project has a newer session on the same ground (" + supersededDay(h.Superseded) + ")"
			if color {
				note = cDim + note + cReset
			}
			fmt.Fprintln(w, note)
		}
		for _, sn := range h.Snippets {
			// Cut before the highlight, not after: the colour codes are not
			// characters the terminal counts, so trimming the rendered string
			// would cut a different number of visible runes on every line.
			fmt.Fprintf(w, "  %s\n", highlight(SafeText(fitLine(sn, o.Width-2)), o.Query, o.Regex, color))
		}
	}
}

// supersededDay prints the newer session's day the way the header above it
// prints days. The marker is the reader's day, as the header's is (#5053), and
// is printed as that day; anything else is printed as it came.
func supersededDay(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	if t.Year() == time.Now().Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

// MatchCount is a session's hit count as the result line prints it: "1 match",
// "3 matches". One spelling, so the line and the width budget fitProject
// measures it against cannot disagree (#4627).
func MatchCount(n int) string {
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
}

// fitProject bounds the one variable-width field on a hit header so the rest of
// the line survives a narrow terminal. Six runes is the floor: below that the
// name says nothing and the reader is better served by the ellipsis alone.
//
// Narrower than the rest of the line needs, the line is left to wrap. What is
// left is the harness, the date, the session id and the count, and the id is
// the field a reader copies into `deja show` — cutting it further would hand
// them a prefix that matches nothing, which is the trap #859 already worded a
// message for.
func fitProject(project string, width int, harness, date, id string, count int, tier string) string {
	if width <= 0 {
		return project
	}
	// The column is padded to ten, so a name shorter than that costs ten either
	// way and the budget has to say so.
	fixed := termwidth.Columns(fmt.Sprintf("[%s]  · %s · %s — %s%s", harness, date, id, MatchCount(count), tier))
	room := max(width-fixed, 10)
	if termwidth.Columns(project) <= room {
		return project
	}
	if room < 6 {
		room = 6
	}
	return termwidth.Cut(project, room-1) + "…"
}

// fitLine cuts a line to the width it is being printed into, counting runes
// rather than bytes. Width zero — piped output, or a terminal that would not
// say — leaves the line alone: a script reading deja's output wants the whole
// thing, and truncating there would lose data rather than layout.
func fitLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	if width < 8 {
		width = 8
	}
	if termwidth.Columns(s) <= width {
		return s
	}
	// One column short of the width, for the ellipsis.
	cut := strings.TrimRight(termwidth.Cut(s, width-1), " ")
	// And back to the end of a word, the same reason the excerpt window snaps
	// its own edges: over sixteen real searches rendered at 120 columns, 118 of
	// the 150 lines this cut shortened (79%) ended inside a word —
	// "kube-prometheus-stack-prome…". The walk is short so a line of unbroken
	// path or code keeps the columns instead.
	if at := strings.LastIndexAny(cut, " \t"); at > 0 && utf8.RuneCountInString(cut[at:]) <= lineSnapRunes {
		cut = strings.TrimRight(cut[:at], " \t")
	}
	return cut + "…"
}

// lineSnapRunes is how much of the last word may be given up to end the line on
// a whole one. A word longer than this is a path or an identifier, where the
// prefix is worth more than the tidy edge.
const lineSnapRunes = 14

func tierLabel(h Hit) string {
	if h.Tier == "" || h.Tier == TierExact {
		return ""
	}
	if h.TierDetail == "" {
		return " · " + h.Tier
	}
	return " · " + h.Tier + " (" + h.TierDetail + ")"
}

func variantDetail(text string, terms []string, variants map[string][]string) string {
	low := lower(text)
	for _, term := range terms {
		for _, variant := range variants[term] {
			// A term that matched itself is an exact hit, not a variant —
			// don't render "connection->connection" as the tier detail.
			if variant == term || variant == "" {
				continue
			}
			if strings.Contains(low, variant) {
				return term + "->" + variant
			}
		}
	}
	return ""
}

// FindByPrefix is the fallback the CLI takes when there is no index to read, so
// it has to answer an empty prefix the same way index.FindByPrefix does: with
// no match. Otherwise the guard depends on whether a rebuild happens to be
// running, which is the worst kind of intermittent.
func FindByPrefix(ss []model.Session, p string) (model.Session, bool) {
	if p == "" {
		return model.Session{}, false
	}
	for _, s := range mergeSessions(ss) {
		if strings.HasPrefix(s.ID, p) {
			return s, true
		}
	}
	return model.Session{}, false
}

// Recent returns the sessions newest first, at most n of them when n > 0.
func Recent(ss []model.Session, n int) []model.Session {
	out := mergeSessions(ss)
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// compileQueryRegex compiles a --re pattern the way grep reads one: ignoring
// case, with ^ and $ anchored at each line. A message is often many lines of
// tool output, and anchoring at the whole text missed `^panic:` on any line
// but the first.
func compileQueryRegex(q string) (*regexp.Regexp, error) {
	return regexp.Compile("(?im)" + q)
}

// A pattern that can match the empty string — `x*`, `foo?`, `(a|)` — matches
// between every pair of characters. Counting those reported 88 matches in a
// session that held none of the pattern's characters at all (#1606); a match
// with no width is not a match.
func countRegex(re *regexp.Regexp, s string) int {
	n := 0
	for _, loc := range re.FindAllStringIndex(s, -1) {
		if loc[1] > loc[0] {
			n++
		}
	}
	return n
}

// TierRelevance mirrors query.TierRelevance for callers of this package.
const TierRelevance = query.TierRelevance

// ErrorHits renders the error-signature tier. The sessions arrive already
// ranked and already narrowed to the neighbourhood of the pasted error, so
// each becomes one hit whose snippets are those messages — re-scoring them
// against the paste's words (as the relevance tier would) throws away the
// passage the tier found and can render a real match as "0 matches".
//
// paste is what the reader pasted. It does not move the excerpt's centre, only
// keeps the lines it carries from being dropped as dumps, and starts the
// excerpt at the first of them.
func ErrorHits(ss []model.Session, paste string) []Hit {
	matched := matchedLine(paste, nil)
	hits := make([]Hit, 0, len(ss))
	for rank, s := range ss {
		hit := Hit{Session: s, Tier: TierError, Count: len(s.Messages), Score: float64(len(ss) - rank)}
		for _, m := range s.Messages {
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			if sn := snippetAt(m.Text, "", nil, matched); sn != "" {
				hit.Snippets = append(hit.Snippets, sn)
				hit.quoted = append(hit.quoted, m.Text)
			}
			if len(hit.Snippets) == 2 {
				break
			}
		}
		hits = append(hits, hit)
	}
	return liftedNotes(hits)
}

// sessionHolds says whether a term appears anywhere in a session, stopping at
// the first message that has it. Joining a session's text into one string to
// ask this is quadratic in the number of messages, and these are transcripts.
func sessionHolds(s model.Session, t string) bool {
	for _, m := range s.Messages {
		low := lower(m.Text)
		if strings.Contains(low, t) {
			return true
		}
		if ft := cjkfold.String(t); ft != t || cjkfold.HasCJK(t) {
			if folded := cjkfold.String(low); strings.Contains(folded, ft) {
				return true
			}
		}
	}
	return false
}

// termWeights says what each query term is worth for picking an excerpt, from
// how much of the answer set holds it.
//
// The index knows the real idf and this layer does not, but it does not need
// to: the question here is only which of two messages in one session to show,
// and a term carried by every session that came back cannot be what separates
// them. A word in one result of fifty is what names that result; a word in all
// fifty is the shape of the question.
//
// Bounded below so a term the whole set shares still counts for something — it
// is weak evidence, not none — and so a message holding two common words can
// still beat one holding a single common word.
func termWeights(ss []model.Session, terms []string) map[string]float64 {
	if len(ss) == 0 {
		return nil
	}
	df := make(map[string]int, len(terms))
	for _, s := range ss {
		for _, t := range terms {
			if sessionHolds(s, t) {
				df[t]++
			}
		}
	}
	out := make(map[string]float64, len(terms))
	for _, t := range terms {
		out[t] = 0.2 + math.Log(float64(len(ss)+1)/float64(df[t]+1))
	}
	return out
}

// RelevanceHits wraps relevance-ranked sessions as hits WITHOUT re-scoring:
// the index already ordered them by IDF overlap, and exact-match BM25 (which
// just failed) must not reshuffle the ranking. Count and snippets come from
// term occurrences so output still shows why each session surfaced.
func RelevanceHits(ss []model.Session, terms []string) []Hit {
	return RelevanceHitsWeighted(ss, terms, nil)
}

// RelevanceHitsWeighted is RelevanceHits told what the ranking judged each term
// to be worth.
//
// The ranking picks a session by the idf mass of its best single message and
// then discards which message that was, so the excerpt has to work out the same
// answer again. Given the ranking's own weights it works out the same one;
// given nothing it estimates them from how much of the answer set holds each
// term, which is close but is an estimate.
func RelevanceHitsWeighted(ss []model.Session, terms []string, idf map[string]float64) []Hit {
	weight := idf
	if weight == nil {
		weight = termWeights(ss, terms)
	}
	hits := make([]Hit, len(ss))
	set := newTermSet(terms)
	// One session's score is read from that session alone, and lowercasing its
	// messages is where the time goes: profiled on a real store, a recall
	// answer spent 2.4 s in it and in the ranker's own copy of the same loop.
	// Spread across cores the way the context renderer already is (#1790) —
	// the work per session is unchanged, so the answer is identical.
	parallelForSessions(len(ss), func(rank int) {
		s := ss[rank]
		hit := Hit{Session: s, Tier: TierRelevance}
		// Snippet the messages where the most query terms MEET, not the first
		// message that contains any one of them. The passage that answers a
		// question is where its words come together — a session about a gift
		// from a sister used to be shown for "what did my dad give me" because
		// "gift" matched first.
		//
		// Terms are worth what they distinguish, not one each. The ranker picks
		// a session by the idf mass of its best message and then discards which
		// message that was; counting terms equally here picked a different one
		// whenever a single rare word carried the session. "How many bikes do I
		// own?" ranked a session on "three bikes" and displayed "many people own
		// one", which is the passage an agent reads before deciding whether the
		// result is worth anything.
		type msgScore struct {
			idx      int
			distinct int
			weighted float64
			center   string
			dump     bool
		}
		best := make([]msgScore, 0, 8)
		found := make([]bool, len(terms))
		var pos []int
		for mi, m := range s.Messages {
			// The same rule the scoring loop applies: a transcript's record of
			// a call to deja is not something anyone said, and this tier is
			// where the live miss came through — the scoring loop was fixed
			// first and the hit arrived here instead (#2067).
			low := lower(withoutOwnCallLog(m.Text))
			pos, _ = set.match(low, found, pos)
			distinct := 0
			weighted := 0.0
			center := ""
			// The rarest term a message holds is what names it, so it is what
			// the excerpt gets centred on.
			heaviest := 0.0
			for ti, t := range terms {
				if !found[ti] {
					continue
				}
				distinct++
				weighted += weight[t]
				if center == "" || weight[t] > heaviest {
					center, heaviest = t, weight[t]
				}
			}
			if distinct > 0 {
				hit.Count++
				best = append(best, msgScore{mi, distinct, weighted, center,
					m.Role == roleToolOutput && isDataDump(m.Text)})
			}
		}
		for _, b := range best {
			hit.matched = append(hit.matched, b.idx)
		}
		// Heaviest first; a stable sort keeps message order among ties. A
		// JSON or listing dump in tool output goes last whatever it weighs:
		// it holds every word as an entry, and a session of them was quoted
		// as `},\n {\n "type": "tool"` (#4780).
		sort.SliceStable(best, func(i, j int) bool {
			if best[i].dump != best[j].dump {
				return !best[i].dump
			}
			return best[i].weighted > best[j].weighted
		})
		for i := 0; i < len(best) && i < 2; i++ {
			if sn := snippet(s.Messages[best[i].idx].Text, best[i].center, nil); sn != "" {
				hit.Snippets = append(hit.Snippets, sn)
				hit.quoted = append(hit.quoted, s.Messages[best[i].idx].Text)
			}
		}
		hit.Score = float64(len(ss) - rank)
		hits[rank] = hit
	})
	out := liftedNotes(hits)
	markEarlierAttempts(out)
	return out
}

// parallelForSessions runs fn over 0..n-1, on one goroutine per core once there
// is enough work to pay for them. The bound is the same shape renderContextTurns
// uses: a handful of items is faster on one core than it is to hand out.
func parallelForSessions(n int, fn func(i int)) {
	workers := min(relevanceWorkers(), n)
	if workers < 2 || n < 4 {
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
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// relevanceWorkers is a variable so a test can force the single-core path and
// compare the two orders.
var relevanceWorkers = defaultRelevanceWorkers

func defaultRelevanceWorkers() int { return runtime.NumCPU() }

// lower is the text fold both sides of a match use: lowercase plus ё→е.
func lower(s string) string { return query.Lower(s) }
