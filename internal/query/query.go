// Package query parses search queries and matches text against them.
// It is a leaf: index and search both build on it.
package query

import (
	"strings"
	"time"
	"unicode"
)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "before": true, "but": true, "by": true,
	"did": true, "do": true, "does": true, "for": true, "from": true,
	"dealt": true,
	"have":  true, "how": true, "in": true, "is": true, "it": true,
	"of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "was": true, "we": true, "what": true,
	"when": true, "where": true, "which": true, "who": true, "with": true,
	// Russian conversational filler — the words that made déjà vu fire on
	// "делай все по шагам" (#313 history). Same bar as the English list:
	// words that identify no task, only грамматика and instruction glue.
	"и": true, "в": true, "во": true, "не": true, "на": true, "но": true,
	"я": true, "ты": true, "он": true, "она": true, "оно": true, "мы": true,
	"вы": true, "они": true, "что": true, "чтобы": true, "как": true,
	"так": true, "это": true, "этот": true, "эта": true, "эти": true,
	"тот": true, "все": true, "всё": true, "всех": true, "был": true,
	"была": true, "было": true, "были": true, "есть": true, "быть": true,
	"будет": true, "для": true, "от": true, "до": true, "по": true,
	"из": true, "у": true, "за": true, "с": true, "со": true, "к": true,
	"ко": true, "о": true, "об": true, "обо": true, "же": true, "ну": true,
	"вот": true, "или": true, "если": true, "когда": true, "где": true,
	"куда": true, "там": true, "тут": true, "здесь": true, "его": true,
	"её": true, "ее": true, "их": true, "мне": true, "меня": true, "тебе": true,
	"тебя": true, "нам": true, "вам": true, "надо": true, "нужно": true,
	"можно": true, "может": true, "давай": true, "сделай": true,
	"делай": true, "делать": true, "сделать": true, "скажи": true,
	"говори": true, "покажи": true, "посмотри": true, "пожалуйста": true,
}

// Options is one search request: what to look for and what to narrow it to.
type Options struct {
	Query                  string
	Regex                  bool
	Harness, Project, Role string
	// Projects is Project for a caller that means one project under several
	// names: the working directory's, whose worktrees each record their own
	// (#3713). Set, it replaces Project in the filter; empty, nothing changes.
	Projects []string
	// ExcludeSessions keeps sessions out by id, whatever they match. The agent
	// surfaces set it to the sessions being written right now: a live transcript
	// is indexed, so the best match for a question asked mid-session can be the
	// question (#3945, #3965). It is honoured in the meta filter rather than on
	// the way out, so a tier that would otherwise be filled by the caller's own
	// session escalates to the next one instead of answering nothing.
	ExcludeSessions map[string]bool
	// ExcludeUpTo keeps out a session's records at or before the time, by id:
	// the turns a fork already holds as its copy of a source that went on
	// after it (#5335).
	ExcludeUpTo map[string]time.Time
	// Session narrows a search to one session, by the id prefix a hit prints.
	// Finding the session by what was said and then searching inside it — for a
	// command, a file, an error — took reopening the transcript by hand (#1321).
	Session string
	// From narrows to the machine a session was worked on: "mini" for what the
	// server did, "local" for this machine's own work. Without it a history
	// gathered from three machines is one undifferentiated pile, and there is
	// no way to ask what any one of them has been doing.
	From  string
	Since time.Duration
	Limit int
	// Sort is the order of the answer: SortDate lists the newest match first,
	// anything else ranks by relevance. Under Window the pool is then the
	// newest matches rather than the best-scored ones, so the page is the
	// newest of the whole match, not the newest of its best thousand (#5577).
	Sort string `json:"-"`
	// Total and Capped travel from RunDetailed to Print so the JSON envelope
	// can report how many sessions matched before the cap, not merely how many
	// survived it. Counting the survivors measures the cap.
	Total  int
	Capped bool
	// TotalEstimated says Total counts sessions retrieval did not read by an
	// estimate (Unread.Estimated).
	TotalEstimated bool
	// Strict is how many of these sessions hold every word of the query. It
	// is nonzero only on the relevance tier, where a thin strict answer is
	// merged into the ranking and published under that label: the tier then
	// says nothing matched while this says how much of it did.
	Strict int
	// PolicyWithheld is how many matching sessions the trust policy kept out
	// of this answer. The reason travelled on stderr only, so a caller reading
	// --json could not tell a rule from an empty history (#990).
	PolicyWithheld int
	// WithAnswer carries the assistant reply next to a matched user turn.
	// Agent-facing recall sets it; human CLI output does not, because a person
	// reading results can open the session.
	WithAnswer bool
	// Width is the terminal the answer is being printed into, so lines can be
	// budgeted rather than assumed 80 columns wide. A 60-column pane is what a
	// split editor gives you, and there every hit wrapped mid-word (#604).
	// Zero means "not a terminal, or unknown": nothing is cut.
	Width                     int
	All, JSON, Fuzzy, Stemmed bool
	NoEmbed                   bool
	Semantic                  bool                `json:"-"`
	SourceInstance            string              `json:"-"`
	FuzzyVariants             map[string][]string `json:"-"`
	Tier                      string              `json:"-"`
	// RecallWorn maps session id -> agent recall count; filled by callers
	// from the usage log, consumed as a bounded ranking boost.
	RecallWorn map[string]int `json:"-"`
	// Now anchors relative-time phrases in the query ("a week ago"); zero
	// means the moment of the search.
	Now time.Time `json:"-"`
	// Window is how many of the best-ranked sessions the caller reads; zero
	// means all of them. Set, retrieval ranks a match much larger than that
	// from the postings and the manifest first and reads only the sessions
	// that can reach the page: scoring every session a common word matched
	// took 22 s and 2.6 GB on a 40k-session store (#5467).
	Window int `json:"-"`
	// Unread describes the matching sessions retrieval left out under Window.
	// Ranking needs their number and length for BM25's corpus figures, and
	// the total needs their number.
	Unread *Unread `json:"-"`
	// Activation is the trust-policy path the answer goes out on (search,
	// mcp). Set, the pool leaves out what the policy withholds there: a
	// denied session took a pool slot from one the page could show, and the
	// unread count reported it as a match.
	Activation string `json:"-"`
	// PoolScope names the projects of a narrower search the caller will cut
	// from this one. Under Window, retrieval also reads every session of
	// theirs that matched, outside the pool, when the narrower search would
	// have read them all; Unread.Scoped lists them and Unread.ScopeWhole says
	// it happened.
	PoolScope []string `json:"-"`
}

// Unread is the part of a match retrieval did not read.
type Unread struct {
	// Sessions is how many matching sessions were left out: as the postings
	// count them when every session read bore them out, scaled from the
	// sample when Estimated. Otherwise the unread ones that pass the text
	// check are read too, and this is zero.
	Sessions int
	// Length is their estimated BM25 length, summed.
	Length float64
	// Sample is a handful of sessions read only to check that estimate, keyed
	// harness:id, each with its estimated length: ranking measures their real
	// length and scales Length by how far off the estimate was. Drawn from
	// outside the pool, where the estimate is applied, because the pool is a
	// biased draw — short sessions rank first.
	Sample map[string]float64
	// Withheld is how many matching sessions the trust policy kept out of
	// the pool under Options.Activation; they are in neither the pool nor
	// Sessions.
	Withheld int
	// Scoped is what Options.PoolScope added to the read, keyed harness:id:
	// matches counted in Sessions above, read only for the narrower answer.
	// Ranking this one leaves them out.
	Scoped map[string]bool
	// ScopeWhole is set when every matching session of Options.PoolScope was
	// read.
	ScopeWhole bool
	// Estimated says Sessions and Withheld were scaled from the sample rather
	// than checked against the text, session by session: the postings
	// over-counted what was read, and the unread ones were too many to check.
	Estimated bool
}

// The orders Options.Sort takes.
const (
	SortRelevance = "relevance"
	SortDate      = "date"
)

// The tiers a hit can come from, strictest first.
const (
	TierExact    = "exact"
	TierClose    = "close"
	TierSemantic = "semantic"
	// TierRelevance ranks sessions by IDF-weighted term overlap when the
	// exact ladder finds nothing — natural-language questions rarely survive
	// an AND over every word.
	TierRelevance = "relevance"
	// TierError answers a pasted error by matching it against stored friction
	// signatures. Unlike relevance, it IS a match — the sessions returned hit
	// the exact error — so it is shown with the error neighbourhood as the
	// snippet, not re-scored against the paste's words.
	TierError = "error"
)

// QueryParts separates ordinary terms from quoted phrases without changing
// the query syntax used by callers.
func QueryParts(q string) (terms []string, phrases []string) { //nolint:revive // exported name used from cmd/deja and internal/index
	start := -1
	var plain strings.Builder
	flushPlain := func() {
		terms = appendUnique(terms, Tokens(plain.String())...)
		plain.Reset()
	}
	for i, r := range q {
		if r != '"' {
			if start < 0 {
				plain.WriteRune(r)
			}
			continue
		}
		if start < 0 {
			flushPlain()
			start = i
			continue
		}
		content := q[start+1 : i]
		if hasLetterOrDigit(content) {
			phrases = appendUnique(phrases, Lower(strings.TrimSpace(content)))
			terms = appendUnique(terms, Tokens(content)...)
		}
		start = -1
	}
	if start >= 0 {
		// An unfinished quote is just whitespace, as it was before phrases.
		return withoutStopWords(Tokens(q)), nil
	}
	flushPlain()
	terms = withoutStopWords(terms)
	return terms, phrases
}

// ProjectWants is the project filter in force: the set where a caller gave
// one, the single --project name otherwise, and nothing for the machine.
func (o Options) ProjectWants() []string {
	if len(o.Projects) > 0 {
		return o.Projects
	}
	if strings.TrimSpace(o.Project) != "" {
		return []string{o.Project}
	}
	return nil
}

// IsStopWord reports whether a token is a query-time stop word. Retrieval
// key selection uses it so a long stop word like "before" cannot displace a
// short content token in the AND intersection.
func IsStopWord(term string) bool { return stopWords[term] }

func withoutStopWords(terms []string) []string {
	kept := make([]string, 0, len(terms))
	for _, term := range terms {
		if !stopWords[term] {
			kept = append(kept, term)
		}
	}
	if len(kept) == 0 {
		return terms
	}
	return kept
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func appendUnique(dst []string, values ...string) []string {
	// Sized on dst alone. The sum is the same map either way — it grows on its
	// own — and adding two lengths is what the allocation-size analyser reads
	// as an overflow candidate.
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range values {
		if v != "" && !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}

// MatchesQuery applies the text-level part of the query. Index candidates
// use this too, so phrase verification is shared by every search frontend.
func MatchesQuery(text, q string) bool {
	terms, phrases := QueryParts(q)
	return MatchesParts(text, terms, phrases, nil)
}

// MatchesParts reports whether text holds every term (or one of its variants)
// and every phrase.
func MatchesParts(text string, terms, phrases []string, variants map[string][]string) bool {
	return MatchesLowered(Lower(text), terms, phrases, variants)
}

// MatchesLowered is MatchesParts for text the caller already put through
// Lower, which is most of what the check costs on a long record.
func MatchesLowered(low string, terms, phrases []string, variants map[string][]string) bool {
	for _, term := range terms {
		matched := strings.Contains(low, term)
		for _, variant := range variants[term] {
			matched = matched || strings.Contains(low, variant)
		}
		if !matched {
			return false
		}
	}
	for _, phrase := range phrases {
		if !strings.Contains(low, phrase) {
			return false
		}
	}
	return len(terms) > 0 || len(phrases) > 0
}

// Tokens lowercases and dedupes the words of a query.
//
// A word is broken by whitespace or by punctuation and symbols outside ASCII,
// and then trimmed of the ASCII punctuation this has always trimmed. The middle
// step is the fix: a query pasted from a chat client or a word processor
// carries typographic punctuation, and it used to stay glued to the word —
// `“retry”`, `—retry—` and `budget…` could not match the plain word the index
// holds, so two of them were rescued by the close-spellings fallback, which
// tells the reader they misspelled something, and `—retry—` missed outright
// (#2117).
//
// ASCII keeps trimming rather than splitting, and this is deliberately not the
// index's own rule (letters, digits, `_`, `-`). Two things depend on it: a
// regex query is words to this function and a pattern to the matcher, so
// splitting `dad|gift` changes which passage an excerpt centres on
// (TestRegexSnippetsStillRankByMatchCount), and an apostrophe inside a word is
// left where the reader typed it.
//
// The length cut stays on bytes: "л" and "舵" are one rune and two or three
// bytes, and a rule that calls them too short to look up is wrong for half the
// world's alphabets (#828).
func Tokens(s string) []string {
	const asciiTrim = "\t\n\r .,;:!?()[]{}<>\"'`"
	broken := strings.FieldsFunc(Lower(s), func(r rune) bool {
		if r < 0x80 {
			return unicode.IsSpace(r)
		}
		// Joiners and variation selectors go with them. They are invisible, and
		// leaving one standing made a heart a query for its selector: it matched
		// every session holding any other emoji spelled with one — a warning
		// sign among them — and never the heart (#2133). Other combining marks
		// stay: they are what tells one Arabic or Hebrew word from another.
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) ||
			unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r)
	})
	seen := map[string]bool{}
	var out []string
	for _, tok := range broken {
		tok = strings.Trim(tok, asciiTrim)
		if len(tok) < 2 || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// CutRecord reports whether a record of session id at t is one ExcludeUpTo
// keeps out. An untimed record of a cut session goes too: nothing places it
// after the fork.
func (o Options) CutRecord(id string, t time.Time) bool {
	cut, ok := o.ExcludeUpTo[id]
	return ok && !t.After(cut)
}
