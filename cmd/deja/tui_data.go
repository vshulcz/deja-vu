package main

import (
	"io"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
)

// tuiRow is one card in the list.
type tuiRow struct {
	s     model.Session
	snips []string // the matched lines, best first
}

// tuiDetail is what the preview and the reader need beyond the index row:
// the whole session, and the line it concluded with.
type tuiDetail struct {
	full        model.Session
	conclusions []string
	gone        bool
	err         error
}

func sessionKey(s model.Session) string { return s.Harness + "\x00" + s.ID }

// tuiSearch runs a query the way `deja search` does, minus everything that
// prints: the tier ladder, the trust policy, the ranking. The rerank and the
// semantic fallback are left out because they are the slow part and the list
// is redrawn on every key.
func tuiSearch(dir string, o search.Options) ([]search.Hit, error) {
	result, err := index.SearchWithRecoveryDetailed(dir, o, io.Discard)
	if err != nil {
		return nil, err
	}
	ss, _ := policyFilterSessionsCounted(policy.ActivationSearch, search.WithoutIgnored(result.Sessions))
	switch result.Tier {
	case search.TierError:
		return search.ErrorHits(ss), nil
	case search.TierRelevance:
		return markStrictHits(search.RelevanceHitsWeighted(ss, index.RelevanceMatchTerms(o.Query), result.TermIDF), result), nil
	}
	o.Tier = result.Tier
	if result.Neighbour || result.Stemmed {
		o.Stemmed = true
		o.FuzzyVariants = result.Variants
	} else if result.Fuzzy {
		o.Fuzzy = true
		o.FuzzyVariants = result.Variants
	}
	if result.Tier == search.TierClose && o.FuzzyVariants == nil {
		o.FuzzyVariants = result.Variants
	}
	d, err := search.RunDetailed(ss, o)
	return d.Hits, err
}

// tuiRecent is the home list: the newest sessions in scope.
func tuiRecent(dir string, projects []string, n int) ([]model.Session, int, error) {
	ss, total, err := index.RecentMatchingCounted(dir, n, search.Options{Projects: projects})
	if err != nil {
		return nil, 0, err
	}
	kept, hidden := policyFilterSessionsCounted(policy.ActivationSearch, ss)
	return kept, total - hidden, nil
}

// tuiKept lists the sessions whose agent has deleted its own copy, newest
// first. It stats every transcript, so it runs once, off the main loop.
func tuiKept(dir string) ([]model.Session, error) {
	ss, _, err := index.RecentMatchingCounted(dir, 0, search.Options{})
	if err != nil {
		return nil, err
	}
	ss, _ = policyFilterSessionsCounted(policy.ActivationSearch, ss)
	var out []model.Session
	for _, s := range ss {
		if !strings.HasPrefix(s.Project, "imported:") && transcriptGone(s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// tuiLoadDetail reads one session whole and pulls out what it concluded.
func tuiLoadDetail(dir string, s model.Session) *tuiDetail {
	full, ok, err := index.FindByIdentity(dir, s.Harness, s.ID)
	d := &tuiDetail{full: s, err: err}
	if ok {
		d.full = full
	}
	d.conclusions = tuiConclusions(d.full, 3)
	d.gone = !strings.HasPrefix(s.Project, "imported:") && transcriptGone(d.full)
	return d
}

// tuiConclusions is the session's answer in a line or three: the same
// extraction the recap uses, cut to whole sentences that fit a card.
func tuiConclusions(s model.Session, n int) []string {
	var out []string
	for _, line := range digest.SubstantialConclusions(s, 1200, n*3) {
		line = strings.Join(strings.Fields(line), " ")
		line = strings.TrimLeft(line, "-*• ")
		if len([]rune(line)) < 12 {
			continue
		}
		dup := false
		for _, o := range out {
			if o == line {
				dup = true
			}
		}
		if !dup {
			out = append(out, line)
		}
		if len(out) >= n {
			break
		}
	}
	return out
}

// tuiAgentCounts counts sessions per harness, biggest first.
func tuiAgentCounts(ss []model.Session) []agentCount {
	m := map[string]int{}
	for _, s := range ss {
		m[s.Harness]++
	}
	out := make([]agentCount, 0, len(m))
	for h, n := range m {
		out = append(out, agentCount{h, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].h < out[j].h
	})
	return out
}

type agentCount struct {
	h string
	n int
}

// tuiAgo is a card's date: relative while it is recent, a date after.
func tuiAgo(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return t.Format("Jan 2")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return num(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return num(int(d.Hours())) + "h ago"
	case d < 14*24*time.Hour:
		return num(int(d.Hours()/24)) + "d ago"
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2 2006")
}

// tuiSnippets is a hit's matched lines, whitespace folded, repeats dropped.
func tuiSnippets(h search.Hit) []string {
	var out []string
	for _, sn := range h.Snippets {
		sn = strings.Join(strings.Fields(sn), " ")
		if sn == "" {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || sameLine(o, sn)
		}
		if !dup {
			out = append(out, sn)
		}
	}
	return out
}

// sameLine says two lines open the same way, which is how a snippet that
// repeats the card's headline gives itself away.
func sameLine(a, b string) bool {
	ra := []rune(strings.ToLower(strings.Join(strings.Fields(a), " ")))
	rb := []rune(strings.ToLower(strings.Join(strings.Fields(b), " ")))
	n := min(len(ra), len(rb), 40)
	return n > 0 && string(ra[:n]) == string(rb[:n])
}
