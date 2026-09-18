package main

import (
	"math"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
)

// suggestFromTitles picks the first-screen suggestion out of what people typed
// as the task, rather than out of the middle of a message.
//
// Measured on a 2,000-session store, four candidate sources scored by what a
// reader running the suggestion gets back and by whether the phrase is one
// somebody wrote as a task (it appears in a hit's own title):
//
//	source                                  hits  in their own title
//	a bigram from any message (the old rule)   18                 0
//	the subject of the friction line           66                 0
//	the newest session's title                 50                 1
//	a phrase recurring across titles           50                 2
//
// Titles win for a structural reason rather than a statistical one: a title is
// what somebody typed when they asked for the work. The friction subject scores
// well and is not used — the brief prints that wall one line above the
// suggestion already, so it would say the same thing twice (#3714).
func suggestFromTitles(dir string) string {
	ss, err := index.Recent(dir, suggestTitleScan)
	if err != nil || len(ss) < 3 {
		return ""
	}
	// The same browsing gate the rest of this screen uses: on a machine whose
	// history arrived by sync, an unfiltered pick prints a peer's phrase (#1352).
	ss, _ = policyFilterSessionsCounted(policy.ActivationSearch, ss)
	if len(ss) < 3 {
		return ""
	}
	titles := make([]string, 0, len(ss))
	for _, s := range ss {
		if t := suggestTitleOf(s); t != "" {
			titles = append(titles, t)
		}
	}
	if len(titles) < 3 {
		return ""
	}
	// Document frequency over the titles, so a word in every task is not
	// distinctive and a word in two is.
	df := map[string]int{}
	pairDF := map[string]int{}
	for _, t := range titles {
		words := titleWords(t)
		seen := map[string]bool{}
		seenPair := map[string]bool{}
		for i, w := range words {
			if !seen[w] {
				seen[w] = true
				df[w]++
			}
			if i+1 < len(words) {
				p := w + " " + words[i+1]
				if !seenPair[p] {
					seenPair[p] = true
					pairDF[p]++
				}
			}
		}
	}
	total := float64(len(titles))
	best, bestScore := "", 0.0
	// Newest first, so an equal score resolves to the most recent task.
	for _, t := range titles {
		words := titleWords(t)
		for i := 0; i+1 < len(words); i++ {
			a, b := words[i], words[i+1]
			if df[a] < 2 || df[b] < 2 {
				continue
			}
			// The pair itself has to recur: a phrase in one title is one
			// session, and this line exists to show that history repeats
			// (#3714, the bar #3715 measured).
			if pairDF[a+" "+b] < minPairSessions(len(titles)) {
				continue
			}
			score := math.Log(total/float64(df[a])) + math.Log(total/float64(df[b]))
			if score > bestScore+1e-9 {
				best, bestScore = a+" "+b, score
			}
		}
	}
	if bestScore < 1.0 {
		return ""
	}
	return best
}

// suggestTitleScan is how far back the titles are read. Titles are short and
// the manifest holds them, so this is cheap next to the message scan the
// fallback does.
const suggestTitleScan = 2000

// suggestTitleOf is a session's title when a person wrote it. An agent-run
// session's title is its own report — on a real store the phrase recurring
// across titles was the closing line of subagent reports — and the same
// predicate recall uses recognises that family.
func suggestTitleOf(s model.Session) string {
	t := strings.TrimSpace(s.Title)
	if t == "" || digest.IsAgentArtifact(t) || digest.IsCompactionSummary(t) {
		return ""
	}
	// The ingest borrows a title from whatever the harness or a tool wrote when
	// a session opens with plumbing — a `<teammate-message>` envelope becomes
	// "harness output: …" and no longer looks like an envelope. That is the
	// family that dominates titles on an agent-heavy store.
	if index.TitleIsBorrowed(t) {
		return ""
	}
	if strings.HasPrefix(t, "<") || strings.HasPrefix(t, "/") {
		// A harness envelope or a slash command is not a task somebody typed.
		return ""
	}
	return t
}

// titleWords is the informative words of a title, in order.
func titleWords(t string) []string {
	raw := query.Tokens(t)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if keep := suggestToken(tok); keep != "" {
			out = append(out, keep)
		}
	}
	return out
}
