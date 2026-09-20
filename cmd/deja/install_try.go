package main

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// Recall happens inside the agent, which is where deja earns its keep and the
// one place a new user never looks. The install said what it wired and then
// offered `deja "something you fixed weeks ago"` — a placeholder the reader has
// to turn into a real question themselves, and the first thing they type is as
// likely to miss as to hit (#575).
//
// So the install ends with a question built from their own history and, before
// it is printed, run: a topic that several of their sessions carry and that one
// of them decided something about. A prompt that misses on the first try is
// worse than no prompt, which is why this verifies rather than guesses.
type tryPrompt struct {
	Question string
	Sessions int
	From     time.Time
	To       time.Time
}

// tryPromptCandidates is how many of the recent sessions are tried before
// giving up, and tryPromptBudget is the whole wall-clock this may spend.
// Install is a command someone is watching: a verified prompt is worth a
// second, not ten. One search per session, measured at 50–100 ms each on a
// 2,422-session store.
const (
	tryPromptCandidates = 20
	tryPromptBudget     = 3 * time.Second
)

// tryPromptMinSessions is "several sessions" from the issue. Two is enough for
// the claim this makes — the agent's answer will carry something the reader
// recognises — and three would silence the screen on a young index.
const tryPromptMinSessions = 2

func buildTryPrompt(dir string) (tryPrompt, bool) {
	if !index.HasManifest(dir) {
		return tryPrompt{}, false
	}
	recent, err := index.Recent(dir, 60)
	if err != nil || len(recent) == 0 {
		return tryPrompt{}, false
	}
	// The same rule the proof block obeys: a machine whose policy keeps
	// imported sessions out of recall must not be handed a prompt that only
	// they answer (#951).
	recent, _ = policyFilterSessionsCounted(policy.ActivationSearch, recent)
	deadline := time.Now().Add(tryPromptBudget)
	tried := 0
	seen := map[string]bool{}
	for _, s := range recent {
		if tried >= tryPromptCandidates || time.Now().After(deadline) {
			break
		}
		// The recent list is built from session metadata and carries no
		// messages, so a decision cannot be looked for here — it is looked for
		// in what the search brings back, which is the text the agent would
		// actually be answering from.
		headline := sessionHeadline(s)
		if pastedHeadline(headline) {
			continue
		}
		words := promptTopicWords(headline)
		if len(words) < 2 {
			continue
		}
		// Two words, and one search per session. Exact retrieval wants every
		// term in the same session, so the more words a topic carries the
		// likelier it matches nothing: on a real 2,422-session store the
		// three- and five-word forms of these titles all came back on the
		// relevance tier — "nothing matched, here is the nearest" — while the
		// two-word form of the same title hit exactly.
		topic := strings.Join(words[:2], " ")
		if seen[topic] {
			continue
		}
		seen[topic] = true
		tried++
		result, err := index.SearchWithRecoveryDetailed(dir, query.Options{Query: topic, All: true}, io.Discard)
		if err != nil {
			continue
		}
		hits, _ := policyFilterSessionsCounted(policy.ActivationSearch, result.Sessions)
		hits = matchedSessions(result, hits)
		if len(hits) < tryPromptMinSessions {
			continue
		}
		// A hit carries only the messages that matched, so the decision has to
		// be looked for in the sessions themselves: asking the hit meant asking
		// whether the matched line happened to be the decision, which is a
		// different and much weaker question.
		if !oneOfThemDecidedSomething(dir, hits) {
			continue
		}
		from, to := sessionSpan(hits)
		return tryPrompt{
			Question: "what did we decide about " + topic + "?",
			Sessions: len(hits),
			From:     from,
			To:       to,
		}, true
	}
	return tryPrompt{}, false
}

// matchedSessions is the part of an answer that matched rather than ranked.
//
// On every tier but relevance that is the whole answer. Relevance means
// nothing matched and these are the nearest sessions deja could find, which is
// the miss this screen exists to avoid — except that a strict answer of fewer
// than ten sessions is published under the same label once the ranking has
// been hung underneath it, and dropping those cost most of a Russian store: of
// 93 two-word topics, 20 came back labelled relevance and every one of them
// held every query word in 1 to 9 sessions (#3815). The ranked tail is still
// not a match, so it is not what the prompt counts or dates.
func matchedSessions(result index.SearchResult, hits []model.Session) []model.Session {
	if result.Tier != "relevance" {
		return hits
	}
	if result.Strict == 0 {
		return nil
	}
	strict := make([]model.Session, 0, result.Strict)
	for _, s := range hits {
		if result.IsStrict(s) {
			strict = append(strict, s)
		}
	}
	return strict
}

// sessionHeadline is what a session is about, as the listings say it: the first
// thing the person typed, or the title deja derived when the metadata is all
// there is.
func sessionHeadline(s model.Session) string {
	if t := firstUserTitle(s); t != "" {
		return t
	}
	return s.Title
}

// oneOfThemDecidedSomething loads the candidate sessions and reports whether
// any of them settled anything. Bounded: the prompt needs one decision, not a
// census, and this runs while somebody watches the install.
func oneOfThemDecidedSomething(dir string, hits []model.Session) bool {
	if len(hits) > tryPromptDecisionReads {
		hits = hits[:tryPromptDecisionReads]
	}
	ids := make([]index.Identity, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, index.Identity{Harness: h.Harness, ID: h.ID})
	}
	full, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return false
	}
	for _, s := range full {
		if sessionSettledSomething(s) {
			return true
		}
	}
	return false
}

// sessionSettledSomething is the shared recogniser, over the side of the
// session that can settle anything.
//
// Not `search.DecisionText`: that answers with the reply's opening when there
// is no decision in it, which is a reasonable thing to show a reader and a
// useless thing to gate on — two sessions that only said "reading the importer
// now" passed it.
func sessionSettledSomething(s model.Session) bool {
	for _, m := range s.Messages {
		switch m.Role {
		case "assistant", "developer":
			if digest.CarriesDecision(strings.ToLower(m.Text)) {
				return true
			}
		}
	}
	return false
}

// tryPromptDecisionReads is how many of the ranked sessions are read in full
// looking for a decision.
const tryPromptDecisionReads = 5

// pastedHeadline reports whether a session's first line is a pasted command or
// URL rather than a subject. A topic lifted out of one reads as noise — on a
// real store the first candidate was two words of an ssh invocation — and the
// question this builds has to sound like something the reader would ask.
func pastedHeadline(title string) bool {
	for _, mark := range []string{"://", " -", "@", "$", "|", "\t", "```"} {
		if strings.Contains(title, mark) {
			return true
		}
	}
	return false
}

// askingWords name no topic, and the query package's stop list does not hold
// them — it is tuned for retrieval, where dropping a term changes what matches,
// and this is only about what the printed question reads like. "what did we
// decide about why job slow now?" is the shape this exists to avoid.
var askingWords = map[string]bool{
	"why": true, "whose": true, "should": true, "could": true, "would": true,
	"почему": true, "зачем": true, "почему-то": true,
}

// promptTopicWords is a session's first line reduced to content words, in the
// order they were written — the reader has to recognise the topic as theirs, so
// nothing is stemmed or reordered. The caller decides how many to use.
func promptTopicWords(title string) []string {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(title)) {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' && r != '/'
		})
		// A dot inside a word is a file name and stays; one at either end is
		// punctuation, and "check." is not the word anybody searches for.
		w = strings.Trim(w, ".")
		if len([]rune(w)) < 3 || query.IsStopWord(w) || askingWords[w] {
			continue
		}
		// A path, a flag or a pasted line is not a topic anybody says out loud.
		if strings.ContainsAny(w, "/\\={}()[]\"'`") {
			continue
		}
		words = append(words, w)
		if len(words) == 5 {
			break
		}
	}
	if len(words) < 2 {
		return nil
	}
	return words
}

func sessionSpan(sessions []model.Session) (time.Time, time.Time) {
	var from, to time.Time
	for _, s := range sessions {
		at := s.Updated
		if at.IsZero() {
			at = s.Started
		}
		if at.IsZero() {
			continue
		}
		if from.IsZero() || at.Before(from) {
			from = at
		}
		if to.IsZero() || at.After(to) {
			to = at
		}
	}
	return from, to
}

// printTryPrompt is the last thing the install says about using deja: a line to
// paste, and where it came from. The provenance line is not decoration — it is
// what tells the reader the question is about their own work rather than an
// example from a README.
func printTryPrompt(w io.Writer, p tryPrompt) {
	fmt.Fprintln(w, "\ntry it now — paste this into your agent:")
	fmt.Fprintf(w, "  %s\n", search.SafeLine(p.Question))
	span := promptSpan(p.From, p.To)
	if span == "" {
		fmt.Fprintf(w, "\n(picked from your history: %d session%s)\n", p.Sessions, pluralS(p.Sessions))
		return
	}
	fmt.Fprintf(w, "\n(picked from your history: %d session%s, %s)\n", p.Sessions, pluralS(p.Sessions), span)
}

func promptSpan(from, to time.Time) string {
	if from.IsZero() || to.IsZero() {
		return ""
	}
	f, t := from.Local().Format("Jan 2006"), to.Local().Format("Jan 2006")
	if f == t {
		return f
	}
	return f + " – " + t
}
