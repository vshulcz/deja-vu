package search

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
)

func scoreBM25(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int) []Hit {
	return scoreBM25At(documents, df, corpusDocuments, avgLength, queryTokenCount, worn, (Options{}).Clock())
}

func scoreBM25At(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int, now time.Time) []Hit {
	emptyQuery := queryTokenCount == 0
	hits := make([]Hit, 0, len(documents))
	for _, doc := range documents {
		score := 0.0
		for i, tf := range doc.termCount {
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(corpusDocuments-df[i])+.5)/(float64(df[i])+.5))
			norm := 1 - bm25B
			if avgLength > 0 {
				norm += bm25B * float64(doc.length) / avgLength
			}
			term := idf * (float64(tf) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm)
			if doc.userCount[i] > 0 {
				term += idf * (float64(doc.userCount[i]) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm) * (userRoleBoost - 1)
			}
			score += term
		}
		if emptyQuery {
			score = float64(doc.hit.Count)
		}
		score *= proximityBoost(doc.minWindow, queryTokenCount)
		score *= titleBoost(doc.titleHits, queryTokenCount)
		// By the id as a log holds it: the counts come from .usage.jsonl, and
		// a byte that is not valid UTF-8 is U+FFFD there and itself here, so
		// the two never met (#2199).
		if n := worn[model.LoggedID(doc.hit.Session.ID)]; n > 0 {
			doc.hit.Reused = n
			score *= wornBoost(n)
		}
		// Curated notes are distilled truth with provenance; they outrank the
		// raw transcripts they were promoted from.
		if doc.hit.Session.Harness == "deja" {
			score *= promotedNoteBoost
		}
		// A session that reached a conclusion outranks one that only discussed
		// the topic. Term frequency alone ranks these backwards: the session
		// where someone kept asking repeats the words most, and the one that
		// answered says them once and then explains. Measured on a corpus built
		// for exactly that shape, the deciding session was ranked last of four
		// in every case before this.
		//
		// A session can both back out one attempt and land another ("reverted
		// the pool cap; the fix was switching to pgx"). That still reached a
		// conclusion, so it earns the boost — the give-up penalty below is what
		// stays clear of it.
		decided := decidesSomething(doc.hit.Session)
		if decided {
			score *= decisionBoost
		}
		// A pasted log outranks a human answer on term frequency alone: it
		// repeats the words fourteen times and says nothing. Measured before
		// this existed, the paste won every question of that shape.
		if looksPasted(doc.hit.Session) {
			score *= pastePenalty
		}
		// A session whose own text says the approach was backed out and reached
		// no other conclusion is the dead end, not the answer: handed to an
		// agent first, it invites repeating what already failed — and the
		// louder it flailed, the higher term frequency ranks it. Demote it
		// below a session that held, without hiding it (the marker still
		// explains it was reverted). Only when it decided nothing else: a
		// session that reverted one thing and settled another keeps the boost
		// above. Skipped only when a person accepted the session afterwards —
		// that is a fresher judgement that overrides the transcript. The other
		// lifecycle states do not rescue it: rejected and stale agree it was a
		// dead end, and superseded means a better record exists, so the give-up
		// penalty still applies. Read the state off the session, not the hit: a
		// hit's Lifecycle is attached by the CLI after ranking, so it is empty
		// here, while a promoted session that arrived by sync carries its state
		// on the session itself.
		if doc.hit.Session.GaveUp && !decided && doc.hit.Session.Lifecycle != "accepted" {
			score *= gaveUpPenalty
		}
		score *= freshnessDecay(doc.hit.Session.Updated, now)
		doc.hit.Score = score
		hits = append(hits, doc.hit)
	}
	sortHits(hits)
	liftNotesAboveTheirSource(hits)
	return hits
}

// liftedNotes applies the note-over-source rule to a tier that ranks by
// position rather than by a score the sort respects, and re-stamps the scores
// so a caller that re-sorts reads the same order back.
//
// The rule lived in the sort, so it reached the tiers that go through it and
// missed the two that build their hits already ranked: a pasted error and the
// "ranked by relevance" screen put a note behind the transcript it was
// distilled from, which is the ordering `promote` prints a promise about
// (#2803).
func liftedNotes(hits []Hit) []Hit {
	liftNotesAboveTheirSource(hits)
	for i := range hits {
		hits[i].Score = float64(len(hits) - i)
	}
	return hits
}

// LiftNotesAboveTheirSource applies the note-over-source rule to a hit list a
// caller outside this package built. The semantic path is such a caller: it
// ranks by cosine alone and never goes through the sort the rule lived in
// (#2803).
func LiftNotesAboveTheirSource(hits []Hit) {
	liftNotesAboveTheirSource(hits)
}

// liftNotesAboveTheirSource keeps a promoted note in front of the transcript it
// was distilled from. The two say the same thing, so nothing is buried by the
// swap — but the note says it in one line with a state attached, and that is
// what `promote` promises. Score alone cannot deliver it: the transcript
// carries the query words in its title and the note does not, and once a note
// is dated by its evidence rather than by the day it was filed (V4) the two are
// equally fresh, so the transcript won its own distillation.
func liftNotesAboveTheirSource(hits []Hit) {
	liftNotesBy(hits, func(h Hit) model.Session { return h.Session })
}

// indexNotes is where each promoted note sits, by the id it names, in rank
// order. Every hit holding an id rather than one of them: two machines can
// promote the same session, and keeping only the last put the peer's copy
// above this machine's own note.
func indexNotes[T any](hits []T, of func(T) model.Session) map[string][]int {
	notes := make(map[string][]int, len(hits))
	for i, h := range hits {
		if id := noteID(of(h)); id != "" {
			notes[id] = append(notes[id], i)
		}
	}
	return notes
}

// noteID is the id a promoted note has on the machine that made it, and "" for
// a session that is not one. Mirrors cmd/deja's promotedNoteID: after a sync
// the local id is `imported-…` and the real one rides in OrigID (#975, #2833).
func noteID(s model.Session) string {
	return s.PromotedNoteID()
}

// liftNotesBy is the rule itself, over anything that names a session: blame
// ranks its own hit type and had the same omission (#2829).
func liftNotesBy[T any](hits []T, of func(T) model.Session) {
	notes := indexNotes(hits, of)
	if len(notes) == 0 {
		return
	}
	// One pass, emitting each source's notes just before it. Reordering in
	// place meant reindexing after every rotation, which is a map rebuild per
	// pair over the whole matched set — measured at 622ms and a gigabyte on a
	// store where every transcript outranked its own note (#2833).
	done := make([]bool, len(hits))
	out := make([]T, 0, len(hits))
	for i := range hits {
		src := of(hits[i])
		if src.Harness != notesHarness {
			// Both ids. The note names the session as it was on the machine
			// that made it — the local id where this machine promoted it, the
			// original where the note travelled — and after a sync a session
			// carries `imported-…` locally with the original in OrigID (#2833).
			//
			// Every copy that has not been placed yet, in rank order, so two
			// machines' notes about one session both land above it and this
			// machine's own comes first.
			for _, id := range src.CandidatePromotedNoteIDs() {
				for _, j := range notes[id] {
					if j > i && !done[j] {
						done[j] = true
						out = append(out, hits[j])
					}
				}
			}
		}
		if !done[i] {
			done[i] = true
			out = append(out, hits[i])
		}
	}
	copy(hits, out)
}

// LiftNoteSessionsAboveTheirSource is the same rule for a caller that ranks
// sessions and never builds a Hit. The per-prompt hook is one: it had no
// note-over-source rule at all, so the transcript a note was distilled from
// went into the block ahead of the note (#2803).
func LiftNoteSessionsAboveTheirSource(ss []model.Session) {
	liftNotesBy(ss, func(s model.Session) model.Session { return s })
}

// sortHits orders a ranked result set.
func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			if hits[i].Session.Updated.Equal(hits[j].Session.Updated) {
				// Same evidence, same moment: prefer this machine's own work.
				// The tie used to fall to the id, and "imported-…" sorts ahead
				// of most session ids — so a peer's copy outranked the local
				// session for no reason anyone chose (#711).
				if li, lj := isLocalProject(hits[i].Session.Project), isLocalProject(hits[j].Session.Project); li != lj {
					return li
				}
				return hits[i].Session.ID < hits[j].Session.ID
			}
			return hits[i].Session.Updated.After(hits[j].Session.Updated)
		}
		return hits[i].Score > hits[j].Score
	})
}

// isLocalProject reports whether a session came from this machine. Imported
// sessions carry the peer prefix the sync path gives them.
func isLocalProject(project string) bool {
	return !model.IsImportedProject(project)
}

// freshnessFloor bounds how much a session's age can cost it. The decay used
// to be 1/(1+age), which keeps 3% of a score after a month and 0.3% after a
// year; BM25 does not span three orders of magnitude, so the exact tier
// ordered by date and let topicality break the ties, the opposite of what it
// says it does (#1269). At a floor of 0.5 age can halve a score, not erase it:
// recency still leads between answers that say the same thing, and a session
// that answers the question outranks a newer one that only mentions it.
const freshnessFloor = 0.5

// freshnessDecay weighs a session's age between freshnessFloor and 1.
func freshnessDecay(updated, now time.Time) float64 {
	if updated.IsZero() {
		return 0
	}
	age := now.Sub(updated).Hours() / 24
	if age <= 0 {
		return 1
	}
	return freshnessFloor + (1-freshnessFloor)/(1+age)
}

// windowScanLimit bounds how many places of one token are enumerated when
// measuring how close the query's words come together. Without it, a long
// transcript message full of a common word makes this quadratic.
//
// Every place inside that bound is weighed. Sampling a subset was the earlier
// shape and it lost the tight cluster whenever all the query's words were
// common: the sample is chosen without knowing where the meeting is (#1319).
const (
	windowScanLimit = 4096
	// windowSegments is how many equal slices of a message get one sampled
	// place each, for a word that runs past the scan limit. A word that common
	// has a place near everything, so which ones are looked at stops mattering
	// — what matters is that the sample covers the whole text rather than its
	// first few kilobytes.
	windowSegments = 64
)

// tokenWindow is the tightest character span in this text that contains every
// query token.
//
// It used to measure between the FIRST place each token appears, which is not a
// window at all: a message that mentions "connection" in its opening line and
// then discusses "connection pool exhausted" as one phrase four paragraphs down
// was scored on the four paragraphs. Proximity is the signal that the words
// belong to one thought, and first-occurrence spacing is nearly the opposite of
// measuring it.
func tokenWindow(low string, qtoks []string) int {
	span, _ := tokenSpan(low, qtoks)
	return span
}

// tokenSpan is tokenWindow plus where that span starts, which is what an
// excerpt has to be centred on to show the words meeting rather than the first
// one to appear.
func tokenSpan(low string, qtoks []string) (span, start int) {
	if len(qtoks) < 2 {
		return 0, 0
	}
	// The span between first occurrences is an upper bound on the real window —
	// it is one way of picking one place per token, and the real answer is the
	// best of all of them. When that bound already reads as one thought, the
	// exact figure cannot change the verdict, only sharpen a boost that is
	// nearly at its ceiling. Enumerating occurrences costs about a fifth of
	// search latency, so it is spent only where the cheap answer says the words
	// are scattered, which is the case it gets wrong.
	first, last := -1, -1
	for _, tok := range qtoks {
		i := strings.Index(low, tok)
		if i < 0 {
			return 0, 0
		}
		if first < 0 || i < first {
			first = i
		}
		if end := i + len(tok); end > last {
			last = end
		}
	}
	if cheap := last - first; cheap <= proximityNear {
		return cheap, first
	}

	type occurrence struct{ at, tok int }
	var occs []occurrence

	// Every place of every token, not a sample of one token's places.
	//
	// The sampled version anchored on the rarest token and asked, for each of 32
	// of its places, where the nearest other tokens were (#1318). That is exact
	// when one query word is rare and blind when they are all common: in a 33 KB
	// message where three query words each appear about eighty times and meet
	// once, in one sentence, it measured 229 against a true 19 — across the
	// boundary where the proximity boost and the excerpt centring live, so the
	// passage that actually answered the question lost both (#1319).
	//
	// Collecting them all is the same walk the sampling already did to find the
	// rarest token, and the sliding window below is linear in what it is handed.
	for ti, tok := range qtoks {
		n := 0
		for i := 0; i < len(low) && n < windowScanLimit; {
			j := strings.Index(low[i:], tok)
			if j < 0 {
				break
			}
			occs = append(occs, occurrence{i + j, ti})
			n++
			i += j + 1
		}
		if n == 0 {
			return 0, 0
		}
		if n < windowScanLimit {
			continue
		}
		// The word runs past the scan limit, so the walk above stopped part way
		// through the message and everything after it is unseen. Replace those
		// places with a sample spread across the whole text plus the last one:
		// the phrase that answers the question sits at the end of a long output
		// as often as anywhere (#1318).
		occs = occs[:len(occs)-n]
		for i, seg := 0, 0; i < len(low) && seg < windowSegments; {
			j := strings.Index(low[i:], tok)
			if j < 0 {
				break
			}
			at := i + j
			occs = append(occs, occurrence{at, ti})
			seg = at*windowSegments/len(low) + 1
			if next := len(low) * seg / windowSegments; next > at {
				i = next
			} else {
				i = at + 1
			}
		}
		if last := strings.LastIndex(low, tok); last >= 0 {
			occs = append(occs, occurrence{last, ti})
		}
	}

	sort.Slice(occs, func(a, b int) bool { return occs[a].at < occs[b].at })

	// Slide a window along the occurrences, shrinking it from the left for as
	// long as it still holds every token.
	seen := make([]int, len(qtoks))
	have, best, left := 0, -1, 0
	bestAt := 0
	for right := range occs {
		if seen[occs[right].tok] == 0 {
			have++
		}
		seen[occs[right].tok]++
		for have == len(qtoks) {
			span := occs[right].at + len(qtoks[occs[right].tok]) - occs[left].at
			if best < 0 || span < best {
				best, bestAt = span, occs[left].at
			}
			seen[occs[left].tok]--
			if seen[occs[left].tok] == 0 {
				have--
			}
			left++
		}
	}
	if best < 0 {
		return 0, 0
	}
	return best, bestAt
}

// proximityNear is the width at which a window stops reading as one thought.
const proximityNear = 200

// proximityBoost rewards documents where the query terms sit together: a
// window under proximityNear chars reads as one thought, a spread across
// kilobytes is coincidence. Bounded at +35%.
func proximityBoost(window, queryTokenCount int) float64 {
	if window <= 0 || queryTokenCount < 2 {
		return 1
	}
	span := float64(window)
	boost := 1 + 0.35*(proximityNear/(proximityNear+span))
	return boost
}

// AbandonmentWorthSaying reports whether the give-up mark tells a reader
// something about this hit. Exported because the MCP recall answer words the
// same mark ("[this session abandoned one approach partway …]") and was asking
// the same question with no rule behind it: over sixteen recall calls on a real
// store it printed the mark 32 times in 16 answers, two per page.
//
// GaveUp is a property of the whole session: one line anywhere saying something
// was dropped marks all of it. On a short session that is useful — "one path
// here was abandoned, check the excerpts for which" names a real thing to look
// for. On a marathon it is certain and therefore empty: measured over twelve
// real queries, the mark sat on 23 of 60 hits, and the sessions carrying it were
// this machine's longest — 4.2 million words, 3.1 million, 2.0 million. Of
// course something was dropped somewhere in four million words.
//
// The same reasoning the rest of this file applies to a haystack: the claim has
// to be about something the reader can act on. So the mark stands on a session
// small enough to scan and goes on one where "somewhere in here" names no place.
//
// Size is the whole test on purpose. The sharper rule — does a give-up line sit
// in the excerpts this hit shows — needs the phrase list that recognises one,
// and that list lives in internal/index, which cannot be imported here.
// Re-spelling it in this package is the mistake #3473 just fixed in the line
// above: two recognisers for one idea, and the narrower one wins by accident.
// A session with no recorded length keeps the mark: an unknown is not a
// marathon.
func AbandonmentWorthSaying(h Hit) bool {
	return h.Session.Words == 0 || h.Session.Words <= abandonmentScannableWords
}

// abandonmentScannableWords is how long a session can be before "somewhere in
// here" stops being a place. Thirty thousand words is a long working session and
// still a thing a reader can search; this machine's marathons are two orders
// above it.
const abandonmentScannableWords = 30000

// lifecycleSummary words a hit's recorded state for a person. It says what
// happened rather than naming the state: "superseded" is our vocabulary, not
// the reader's.
func lifecycleSummary(h Hit) string {
	var head string
	switch h.Lifecycle {
	case "rejected":
		head = "tried and rejected"
	case "superseded":
		head = "replaced by a later decision"
	case "stale":
		head = "marked stale — may no longer hold"
	default:
		head = SafeLine(h.Lifecycle)
	}
	// LifecycleAt and LifecycleNote are free text a peer may have synced; this
	// line prints to a terminal, so strip them like blame's lifecycle line.
	if h.LifecycleAt != "" {
		head += " (" + SafeLine(h.LifecycleAt) + ")"
	}
	if h.LifecycleNote != "" {
		head += ": " + SafeNote(h.LifecycleNote)
	}
	return head
}

// decisionBoost is sized to the effect it has to overcome, not tuned until a
// test passed: a session that repeats the query terms about three times more
// than another scores roughly twice as high once BM25 saturation is accounted
// for, and that is the ordinary shape of "someone kept asking" against "someone
// answered". It stays a tie-breaker rather than an override — a conclusion
// about a different topic must not outrank a session that squarely matches the
// question, and there is a test for exactly that.
const decisionBoost = 2.0

// roleToolOutput mirrors sources.RoleToolOutput; this package sits below it.
const roleToolOutput = "tool-output"

// roleSummary is a harness's digest of a conversation it compacted away. Kept
// in the index — it is the only record of the turns that went — and served only
// when a caller asks for it by role (#3384).
const roleSummary = "summary"

// decidesSomething reports whether a session contains an answer rather than a
// conversation about one. It looks only at non-user turns: a user writing "we
// should just pin it" is a proposal, and the same words from the side that did
// the work are a record of what happened.
func decidesSomething(s model.Session) bool {
	for _, m := range s.Messages {
		// Tool output is not the assistant concluding something. Before #559 it
		// arrived labelled `user` and was skipped here by accident; now it is
		// labelled honestly and has to be skipped on purpose.
		if m.Role == "user" || m.Role == "" || m.Role == roleToolOutput {
			continue
		}
		low := lower(m.Text)
		for _, phrase := range decisionPhrases {
			if strings.Contains(low, phrase) {
				return true
			}
		}
	}
	return false
}

// pastePenalty damps a session whose matched text is a dump rather than a
// conversation. It is deliberately mild: some pastes are the answer — a stack
// trace someone diagnosed, a config that turned out to be wrong — so this
// lowers them behind real discussion rather than hiding them.
const pastePenalty = 0.5

// gaveUpPenalty damps a session whose own text reports the approach was backed
// out. Same magnitude as pastePenalty and for the same reason: it lowers the
// dead end behind a conclusion that held, but does not hide it — "we tried this
// and reverted" is worth reading, second, so an agent learns what not to redo.
const gaveUpPenalty = 0.5

// pasteMinLines is the length below which repetition means nothing. Three
// identical short lines are a list; fourteen are a log.
const pasteMinLines = 8

// pasteDistinctRatio is the share of distinct lines below which a message reads
// as machine output. A conversation almost never repeats two thirds of itself.
const pasteDistinctRatio = 0.35

// looksPasted reports whether a session's text is dominated by repeated lines.
// Line repetition rather than vocabulary: a log repeats its shape, and a person
// explaining something at length does not.
func looksPasted(s model.Session) bool {
	dump, total := 0, 0
	for _, m := range s.Messages {
		lines := strings.Split(strings.TrimSpace(m.Text), "\n")
		if len(lines) < pasteMinLines {
			continue
		}
		total++
		distinct := make(map[string]struct{}, len(lines))
		for _, l := range lines {
			distinct[strings.TrimSpace(l)] = struct{}{}
		}
		if float64(len(distinct))/float64(len(lines)) < pasteDistinctRatio {
			dump++
		}
	}
	return total > 0 && dump*2 >= total
}

// promotedNoteBoost lifts curated deja notes over raw transcripts on equal
// relevance. Kept modest: a note about X must not bury a transcript about Y.
const promotedNoteBoost = 1.25

// wornBoost rewards sessions agents keep recalling. The shape is set by
// measurement (internal/search reuse tests): at the old
// 0.05 slope / 1.2 cap the boost rescued a reused answer only when a louder
// session tied it exactly — a single extra term of noise buried it, so the
// signal was nearly inert. A 0.10 slope and 1.5 cap let reuse surface an answer
// out-matched by a couple of terms, while a heavily-reused near-miss still loses
// to a strong match: relevance stays dominant, reuse breaks more than dead ties.
func wornBoost(n int) float64 {
	boost := 1 + 0.10*math.Log2(float64(1+n))
	if boost > 1.5 {
		return 1.5
	}
	return boost
}

// titleBoost rewards query tokens appearing in the session title — the
// strongest single relevance signal a session carries. Bounded at +40%.
func titleBoost(titleHits, queryTokenCount int) float64 {
	if titleHits == 0 || queryTokenCount == 0 {
		return 1
	}
	return 1 + 0.4*float64(titleHits)/float64(queryTokenCount)
}

func countDocumentWords(s string, terms []string, variants map[string][]string, counts, userCounts []int, user bool) int {
	length := 0
	start := -1
	// cjk counts the characters of a run written in a script without spaces, and
	// other counts the rest of that run, so a mixed run like "scheduler調度器"
	// contributes both. Counted here rather than in a second pass over the word:
	// this runs over every message of every candidate on every search.
	cjk, other := 0, 0
	for i := 0; i <= len(s); {
		isWord := false
		size := 0
		if i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			size = n
			isWord = unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
			if isWord {
				if cjkfold.Unspaced(r) {
					cjk++
				} else {
					other++
				}
			}
		}
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			word := s[start:i]
			// Length feeds BM25's normalisation, the part that stops a long
			// document mentioning a term once from outranking a short one about
			// it. Chinese, Japanese and Korean put no spaces between words, so a
			// four-thousand-character aside was one word long and normalisation
			// had nothing to work with: measured on the same content, a passing
			// mention was separated from the answer by 6.2x in English and 1.3x
			// in Chinese (#1344). Their characters are the words.
			if cjk > 0 {
				length += cjk
				if other > 0 {
					length++
				}
			} else {
				length++
			}
			cjk, other = 0, 0
			for j, term := range terms {
				matched := word == term
				if !matched {
					// A word matching a fuzzy/stem variant of this term counts
					// toward its term frequency so BM25 ranks the hit instead of
					// falling back to recency-only order.
					if slices.Contains(variants[term], word) {
						matched = true
					}
				}
				if matched {
					counts[j]++
					if user {
						userCounts[j]++
					}
				}
			}
			start = -1
		}
		if i == len(s) {
			break
		}
		i += size
	}
	return length
}
