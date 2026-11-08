package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/query"
)

func fuzzyPostings(dir string, terms, phrases []string) ([]posting, map[string][]string, error) {
	var idx *tokenIndex
	if !hasFuzzyToken(terms, nil) {
		// Every term is below the length floor. One of them may still be a
		// word whose marked form is in the index, which only the catalog can
		// say — but a mark strips to a non-ASCII base, so an ASCII term has
		// none and the catalog stays unread. That keeps the answer this path
		// gave before it asked, and keeps it free (#2898).
		if !anyNonASCII(terms) {
			return nil, nil, nil
		}
		cat, err := tokenIndexCached(dir)
		// A catalog that will not read means no, as it did before.
		if err != nil || !hasFuzzyToken(terms, cat) {
			return nil, nil, nil //nolint:nilerr // a catalog that will not read means no
		}
		idx = cat
	}
	if idx == nil {
		cat, err := tokenIndexCached(dir)
		if err != nil {
			return nil, nil, err
		}
		idx = cat
	}
	perToken := make([]map[int64]posting, len(terms))
	variants := map[string][]string{}
	for i, term := range terms {
		// A term the corpus spells exactly is not a typo, and this tier exists
		// for typos. One bucket read settles it; the candidate walk behind
		// closeTokens compares the term against every token of a similar
		// length and was running for words like "rice" and "favorite" that
		// the index already holds verbatim.
		// A word the corpus already uses everywhere gains nothing from its
		// neighbours: it is not a typo, and widening it only adds postings
		// that discriminate nothing. A rare word is still worth widening even
		// when it is spelled correctly, because a near-neighbour of a rare
		// word is usually the same thing said differently.
		var matches []string
		if exact, eerr := postingsFor(dir, "t"+term); eerr == nil && len(exact) >= commonTokenPostings {
			matches = []string{term}
		} else {
			matches = closeTokens(term, idx)
		}
		if len(matches) == 0 {
			return nil, nil, nil
		}
		variants[term] = matches
		perToken[i] = map[int64]posting{}
		for _, variant := range matches {
			posts, err := postingsFor(dir, "t"+variant)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range posts {
				perToken[i][p.Off] = p
			}
		}
	}
	// Phrase text is verified from records; phrase tokens participate in the
	// same fuzzy candidate intersection above, so phrases need no extra work.
	_ = phrases
	return intersectPostingMaps(perToken), variants, nil
}

func fuzzySearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	terms, phrases := query.QueryParts(o.Query)
	posts, variants, err := fuzzyPostings(dir, terms, phrases)
	if err != nil || len(posts) == 0 {
		return SearchResult{}, err
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return SearchResult{}, nil
	}
	ss, err := scanRecordsWithVariants(dir, m, o, postingOffsets(posts), variants)
	if err != nil || len(ss) == 0 {
		return SearchResult{}, err
	}
	return SearchResult{Sessions: ss, Fuzzy: true, Variants: variants, Tier: query.TierClose}, nil
}

func stemSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	return stemSearchFor(dir, m, o, 0)
}

// loneWordSearch is the stem rung for a two-word question one of whose words
// no session holds: "kafka queue" against a store that only ever said queue.
// A longer question drops such a word and keeps the AND over the rest; two
// words had no rest to keep, so a typo, a synonym or a translated word emptied
// the page (#5711). It runs only when every other rung came back empty, and
// answers only when the word left standing is informative and the caller
// asked for it (o.LoneWord, agent recall).
func loneWordSearch(dir string, m Manifest, o query.Options) (SearchResult, error) {
	if !o.LoneWord || len(m.Sessions) == 0 {
		return SearchResult{}, nil
	}
	if terms, _ := query.QueryParts(o.Query); len(terms) != 2 {
		return SearchResult{}, nil
	}
	res, err := stemSearchFor(dir, m, o, len(m.Sessions))
	res.LoneWord = res.Stemmed
	return res, err
}

// stemSearchFor is the stem rung; sessions > 0 lets a two-word question fall
// back to its one informative word (stemPostingsFor).
func stemSearchFor(dir string, m Manifest, o query.Options, sessions int) (SearchResult, error) {
	terms, phrases := query.QueryParts(o.Query)
	posts, variants, err := stemPostingsFor(dir, terms, phrases, sessions)
	if err != nil || len(posts) == 0 {
		return SearchResult{}, err
	}
	posts = cutPostingsBySession(posts, m, o)
	if len(posts) == 0 {
		return SearchResult{}, nil
	}
	ss, err := scanRecordsWithVariants(dir, m, o, postingOffsets(posts), variants)
	if err != nil || len(ss) == 0 {
		return SearchResult{}, err
	}
	return SearchResult{Sessions: ss, Stemmed: true, Variants: variants, Tier: query.TierClose}, nil
}

// loneWordInforms is whether the one word left of a two-word question may
// answer for it: not filler, and rare by the bar the co-occurrence rung holds
// its words to, so the page is the sessions about that word, not the store.
func loneWordInforms(term string, set map[int64]posting, sessions int) bool {
	if query.IsStopWord(term) {
		return false
	}
	in := map[uint32]bool{}
	for _, p := range set {
		in[p.Sid] = true
	}
	return len(in) <= 2 || rankIDF(sessions, len(in)) >= dejaVuIDFFloor
}

// stemPostingsFor is stemPostings that, given the store's session count, also
// lets a two-word question fall back to its one informative word.
func stemPostingsFor(dir string, terms, phrases []string, sessions int) ([]posting, map[string][]string, error) {
	lone := sessions > 0 && len(terms) == 2
	if !lone && !hasStemToken(terms) {
		return nil, nil, nil
	}
	catalog, err := tokenIndexCached(dir)
	if err != nil {
		return nil, nil, err
	}
	matchesPer := make([][]string, len(terms))
	for i, term := range terms {
		matchesPer[i] = stemMatches(term, catalog.set)
	}
	// Whatever the catalog has no whole word for gets one pass looking for the
	// word inside a token that glued it to something else. One pass for all of
	// them: the catalog is ~200k tokens on a real store, which is the walk the
	// fuzzy tier goes out of its way not to do per term.
	fillGluedMatches(terms, matchesPer, catalog)
	anchored := 0
	for i := range terms {
		if len(matchesPer[i]) > 0 {
			anchored++
		}
	}
	if anchored == 0 {
		return nil, nil, nil
	}
	// A token with no occurrences anywhere in the corpus cannot anchor the
	// AND — natural-language queries are full of them. Drop such tokens when
	// at least two anchored terms remain; the empty-string variant marks the
	// token optional for the scan-time matcher.
	variants := map[string][]string{}
	type anchor struct {
		term string
		set  map[int64]posting
	}
	anchors := make([]anchor, 0, len(terms))
	for i, term := range terms {
		if len(matchesPer[i]) == 0 {
			if anchored < 2 && !lone {
				return nil, nil, nil
			}
			variants[term] = []string{""}
			continue
		}
		variants[term] = matchesPer[i]
		set := map[int64]posting{}
		for _, variant := range matchesPer[i] {
			posts, err := postingsFor(dir, "t"+variant)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range posts {
				set[p.Off] = p
			}
		}
		anchors = append(anchors, anchor{term: term, set: set})
	}
	_ = phrases
	if lone && len(anchors) == 1 {
		if !loneWordInforms(anchors[0].term, anchors[0].set, sessions) {
			return nil, nil, nil
		}
		return intersectPostingMaps([]map[int64]posting{anchors[0].set}), variants, nil
	}
	sets := func(skip map[int]bool) []map[int64]posting {
		out := make([]map[int64]posting, 0, len(anchors))
		for i, a := range anchors {
			if !skip[i] {
				out = append(out, a.set)
			}
		}
		return out
	}
	if posts := intersectPostingMaps(sets(nil)); len(posts) > 0 {
		return posts, variants, nil
	}
	// Best-effort AND: no single session holds every anchored token. Natural
	// queries carry filler ("why", "let") — try dropping up to two tokens,
	// shortest first, and keep the first combination that matches anything.
	if len(anchors) < 3 {
		return nil, variants, nil
	}
	order := make([]int, len(anchors))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len(anchors[order[a]].term) < len(anchors[order[b]].term)
	})
	for _, i := range order {
		if posts := intersectPostingMaps(sets(map[int]bool{i: true})); len(posts) > 0 {
			variants[anchors[i].term] = []string{""}
			return posts, variants, nil
		}
	}
	if len(anchors) >= 4 {
		for x := range order {
			for y := x + 1; y < len(order); y++ {
				i, j := order[x], order[y]
				if posts := intersectPostingMaps(sets(map[int]bool{i: true, j: true})); len(posts) > 0 {
					variants[anchors[i].term] = []string{""}
					variants[anchors[j].term] = []string{""}
					return posts, variants, nil
				}
			}
		}
	}
	return nil, variants, nil
}

func hasStemToken(terms []string) bool {
	for _, term := range terms {
		if len([]rune(term)) >= 5 {
			return true
		}
	}
	return false
}

// gluedCompared counts the tokens the glued step held up against a form, for
// the test that pins it to the question rather than the catalog (#5386).
var gluedCompared atomic.Int64

// fillGluedMatches finds a term's other forms inside tokens that glued the
// word to something else: "k8sнастройки" holds "настройки" and is one token,
// so nothing splits it and stemMatches, which asks the catalog for whole
// words, comes back empty. The reader then has to type the case the transcript
// used, in the one place deja normally spares them that (#2145).
//
// Only when the whole-word lookup found nothing, only at the ends of the
// token, only for forms of four runes or more, and only for Cyrillic terms.
// That last one is the point rather than caution: Russian is where a word has
// a dozen endings and the reader cannot be expected to guess which one the
// transcript used, while in Latin the same rule reaches "latest" from "tests"
// and "preserve" from "press". English compounds have their own road — the
// indexer splits them into parts — and this one would only add coincidences.
func fillGluedMatches(terms []string, matchesPer [][]string, catalog *tokenIndex) {
	formsPer := make([][]string, len(terms))
	want := false
	for i, term := range terms {
		if len(matchesPer[i]) > 0 || len([]rune(term)) < 5 || !isCyrToken(term) {
			continue
		}
		for _, f := range append(stemMatchForms(term), term) {
			// Four runes is the floor: a shorter fragment at the end of a long
			// token is a coincidence more often than a word.
			if len([]rune(f)) >= 4 {
				formsPer[i] = append(formsPer[i], f)
				want = true
			}
		}
	}
	if !want {
		return
	}
	// The token has to be the word plus something, and the something is what
	// makes this rung necessary — so the length is measured against the form,
	// not against the term, which is the longer of the two when the query is
	// in a case with a long ending. A token that starts or ends with the form
	// and is longer in bytes is longer in runes too, since the form ends on a
	// whole rune.
	//
	// Looked up by the form's first and last four runes rather than by walking
	// the catalog once per form: the walk counted the runes of every token in
	// the store for each of a Cyrillic term's hundred-odd forms, which was most
	// of a recall's time on a 12k-session store and grew with it (#5386).
	glued := catalog.gluedKeys()
	found := make([]map[string]bool, len(terms))
	for i, forms := range formsPer {
		for _, f := range forms {
			if isASCIIString(f) || !utf8.ValidString(f) {
				// The keys are taken over non-ASCII tokens by whole runes,
				// which is no way to find an ASCII form or a broken one;
				// those are walked for under the old rule.
				for tok := range catalog.set {
					gluedCompared.Add(1)
					if len([]rune(tok)) > len([]rune(f)) && (strings.HasPrefix(tok, f) || strings.HasSuffix(tok, f)) {
						if found[i] == nil {
							found[i] = map[string]bool{}
						}
						found[i][tok] = true
					}
				}
				continue
			}
			heads, tails := glued.head[runeHead(f, gluedKeyRunes)], glued.tail[runeTail(f, gluedKeyRunes)]
			gluedCompared.Add(int64(len(heads) + len(tails)))
			for _, tok := range heads {
				if len(tok) > len(f) && strings.HasPrefix(tok, f) {
					if found[i] == nil {
						found[i] = map[string]bool{}
					}
					found[i][tok] = true
				}
			}
			for _, tok := range tails {
				if len(tok) > len(f) && strings.HasSuffix(tok, f) {
					if found[i] == nil {
						found[i] = map[string]bool{}
					}
					found[i][tok] = true
				}
			}
		}
	}
	for i, set := range found {
		if len(set) == 0 {
			continue
		}
		out := make([]string, 0, len(set))
		for tok := range set {
			out = append(out, tok)
		}
		sort.Strings(out)
		if len(out) > 8 {
			out = out[:8]
		}
		matchesPer[i] = out
	}
}

func stemMatches(term string, catalog map[string]bool) []string {
	var forms []string
	if len([]rune(term)) < 5 {
		for _, form := range []string{term, term + "s", term + "es", strings.TrimSuffix(term, "s")} {
			if len(form) >= 3 {
				forms = append(forms, form)
			}
		}
		forms = append(forms, doubledForms(term)...)
	} else {
		forms = suffixForms(term)
	}
	forms = append(forms, devSynonyms[term]...)
	forms = append(forms, cyrSuffixForms(term)...)
	matches := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, form := range forms {
		if !seen[form] && catalog[form] {
			seen[form] = true
			matches = append(matches, form)
		}
	}
	if len(matches) > 8 {
		matches = matches[:8]
	}
	return matches
}

// devSynonyms is a small reviewed fold table for the abbreviations developers
// actually type. Deterministic and shipped in the repo — no embeddings, no
// guessing. Applied only in the stem tier (exact matches never consult it)
// and narrated like any other variant.
var devSynonyms = func() map[string][]string {
	pairs := [][2]string{
		{"auth", "authentication"}, {"auth", "authorization"},
		{"db", "database"}, {"k8s", "kubernetes"},
		{"env", "environment"}, {"config", "configuration"},
		{"cfg", "config"}, {"repo", "repository"},
		{"perm", "permission"}, {"cert", "certificate"},
		{"dir", "directory"}, {"msg", "message"},
		{"deps", "dependencies"}, {"prod", "production"},
		{"param", "parameter"}, {"arg", "argument"},
		{"docs", "documentation"}, {"err", "error"},
		{"regex", "regexp"}, {"spec", "specification"},
	}
	m := map[string][]string{}
	for _, p := range pairs {
		m[p[0]] = append(m[p[0]], p[1])
		m[p[1]] = append(m[p[1]], p[0])
	}
	return m
}()

// cyrEndings are common Russian inflection endings, longest first so the
// stem strips greedily. A bounded fold, not a morphology engine.
var cyrEndings = []string{
	"иями", "ями", "ами", "ией", "иях", "ях", "ах", "ов", "ев", "ей",
	"ой", "ий", "ия", "ию", "ии", "ие", "ый", "ая", "ое", "ые",
	"ью", "ям", "ем", "ом",
	"ть", "л", "ла", "ло", "ли", "а", "я", "у", "ю", "ы", "и", "е", "о", "ь",
}

// endsInfinitive reports whether a ть-final term is shaped like a verb: the
// rune before "ть" is a vowel. Consonant-stem infinitives (лезть, сесть,
// класть) are misread as nouns and lose the verb branch — 11 reachable pairs
// across a 20k frequency list, against closing часть -> час and весть -> вес,
// which are common words landing on a different lemma entirely.
func endsInfinitive(term string) bool {
	if !strings.HasSuffix(term, "ть") {
		return false
	}
	runes := []rune(term)
	if len(runes) < 3 {
		return false
	}
	switch runes[len(runes)-3] {
	case 'а', 'е', 'ё', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// cyrSoftEndings is the third-declension feminine paradigm, and the only set
// that may attach to a stem exposed by stripping a soft sign. Attaching the
// hard adjective endings there is what folded цель onto целая and верь onto
// вера: сеть->сети needs "и", цель->целая needs "ая", so keeping the two
// tables apart separates shapes that look identical by length alone.
var cyrSoftEndings = []string{"и", "ью", "ям", "ях", "ями", "ей", "е", "я", "ю", "ем"}

// cyrVerbEndings are the infinitive and past-tense endings.
var cyrVerbEndings = map[string]bool{"ть": true, "л": true, "ла": true, "ло": true, "ли": true}

// cyrVerbEndingList is what a ь-final infinitive's stem takes back: the past
// tense plus the present-tense vowels, so знать reaches both знал and знаю.
var cyrVerbEndingList = []string{"ть", "л", "ла", "ло", "ли", "ю", "у", "а", "я", "е", "и", "ем"}

// cyrNominalEndings are unambiguously noun-case: a stem exposed by stripping
// one of these came from a noun, so verb endings must not attach to it —
// весом strips to вес, and вес+ть is весть. Endings a verb can also carry
// (ю, у, а, и ...) stay out of this set, so знаю -> зна -> знать survives.
var cyrNominalEndings = map[string]bool{
	"иями": true, "ями": true, "ами": true, "ией": true, "иях": true,
	"ях": true, "ах": true, "ов": true, "ев": true, "ей": true, "ой": true,
	"ий": true, "ия": true, "ию": true, "ии": true, "ие": true,
	"ый": true, "ая": true, "ое": true, "ые": true, "ом": true,
}

// cyrMatchForms folds a Russian term onto its inflection family for the
// relevance tier: strip a known ending, then re-attach the endings that
// belong to the same paradigm. Four runes is the floor — below it the
// three-letter function words (что, как, его) would fan out for nothing.
//
// The tier has no catalog gate, so whatever this invents is looked up for
// real and can surface a session. That is why stripping and re-attaching use
// different tables: one shared table folded цель onto целая.
func cyrMatchForms(term string) []string {
	runes := []rune(term)
	if len(runes) < 4 {
		return nil
	}
	seen := map[string]bool{term: true}
	forms := make([]string, 0, len(cyrEndings)+1)
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			forms = append(forms, f)
		}
	}
	if before, ok := strings.CutSuffix(term, "ь"); ok {
		// Soft stem — the third-declension nouns: сеть, жизнь, ночь, очередь.
		if base := before; len([]rune(base)) >= 3 {
			// The bare stem is deliberately not emitted: a third-declension
			// noun's stem is not a word on its own (сеть -> сет), while for a
			// ь-final imperative it is — борись would recall Борис.
			for _, end := range cyrSoftEndings {
				add(base + end)
			}
		}
		// A ь-final infinitive is a verb, not a noun: знать -> знал. But a
		// noun can end in "ть" too, and часть taking the verb branch reaches
		// час — a different lemma entirely. Infinitives are vowel + ть
		// (де-лать, зна-ть, ви-деть); ть-final nouns are consonant + ть
		// (час-ть, вес-ть, лес-ть, кос-ть). That also closes весть -> вес,
		// which was the surviving inverse of весом -> весть.
		if strings.HasSuffix(term, "ть") && endsInfinitive(term) {
			if base := strings.TrimSuffix(term, "ть"); len([]rune(base)) >= 3 {
				add(base)
				for _, end := range cyrVerbEndingList {
					add(base + end)
				}
			}
		}
		return forms
	}
	base, stripped := term, ""
	for _, end := range cyrEndings {
		if strings.HasSuffix(term, end) && len(runes)-len([]rune(end)) >= 3 {
			base, stripped = strings.TrimSuffix(term, end), end
			break
		}
	}
	if base != term {
		add(base)
	}
	nominal := cyrNominalEndings[stripped]
	for _, end := range cyrEndings {
		if nominal && cyrVerbEndings[end] {
			continue
		}
		add(base + end)
	}
	return forms
}

// cyrSuffixForms bridges Russian inflection: strip the longest known ending,
// then re-attach each — миграция matches миграции and миграцию. ASCII terms
// return nothing.
func cyrSuffixForms(term string) []string {
	if !isCyrToken(term) {
		return nil
	}
	// Both tiers fold Russian the same way. This one used to strip and
	// re-attach from one shared table, which is the цель->целая shape the
	// relevance tier was fixed for — it survived here one tier over, letting
	// пусть recall a session that only says пустой. The catalog gate and the
	// 8-match cap made it milder, not correct.
	return cyrMatchForms(term)
}

func suffixForms(word string) []string {
	seen := map[string]bool{word: true}
	type candidate struct {
		word  string
		depth int
	}
	queue := []candidate{{word: word}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth == 2 {
			continue
		}
		for _, form := range oneSuffixStep(current.word) {
			if form != "" && !seen[form] {
				seen[form] = true
				queue = append(queue, candidate{word: form, depth: current.depth + 1})
			}
		}
	}
	out := make([]string, 0, len(seen))
	for form := range seen {
		out = append(out, form)
	}
	sort.Strings(out)
	return out
}

func oneSuffixStep(word string) []string {
	var out []string
	add := func(form string) {
		if len(form) >= 3 && form != word {
			out = append(out, form)
		}
	}
	switch {
	case strings.HasSuffix(word, "tion"):
		add(strings.TrimSuffix(word, "tion") + "te")
	case strings.HasSuffix(word, "ing"):
		base := strings.TrimSuffix(word, "ing")
		add(base)
		add(base + "e")
		add(undoubled(base))
	case strings.HasSuffix(word, "ies"):
		// retries -> retry. Ahead of the "es" and "s" cases, which reduce it
		// to "retri" and stop there: neither a consonant+y word nor its
		// plural could reach the other, so "retry" found nothing in a store
		// full of "retries" (#1079).
		add(strings.TrimSuffix(word, "ies") + "y")
		add(strings.TrimSuffix(word, "es"))
		// And the plain "s", for the reason the "es" case below takes it:
		// "movies" is neither "movy" nor "movi", and nothing further down
		// strips it again (#2137).
		add(strings.TrimSuffix(word, "s"))
	case strings.HasSuffix(word, "ied"):
		add(strings.TrimSuffix(word, "ied") + "y")
	case strings.HasSuffix(word, "ed"):
		base := strings.TrimSuffix(word, "ed")
		add(base)
		add(base + "e")
		add(undoubled(base))
	case strings.HasSuffix(word, "ment"):
		base := strings.TrimSuffix(word, "ment")
		add(base)
		add(base + "e")
	case strings.HasSuffix(word, "es"):
		// Both strips, because the switch stops here and the plain "s" case
		// below never runs for these words: "boxes" needs the "es" gone and
		// "pipelines" needs only the "s", and a plural of every noun ending in
		// "e" reached the stub "pipelin" and no further. Whichever form the
		// store does not hold falls out at the catalog, which every lookup
		// goes through; the relevance keys take the forms unfiltered, and there
		// the extra one is the real singular (#2137).
		add(strings.TrimSuffix(word, "es"))
		add(strings.TrimSuffix(word, "s"))
	case strings.HasSuffix(word, "s"):
		add(strings.TrimSuffix(word, "s"))
	}
	if before, ok := strings.CutSuffix(word, "e"); ok {
		base := before
		add(base + "ing")
		add(base + "ed")
		add(strings.TrimSuffix(word, "te") + "tion")
	}
	// expansions: fail->fails, fail->failing/failed. The catalog filter keeps
	// nonsense forms from ever reaching a lookup.
	if base, ok := consonantY(word); ok {
		// retry -> retries, retried. The plain +s below gives "retrys", which
		// no transcript contains (#1079).
		add(base + "ies")
		add(base + "ied")
	}
	if !strings.HasSuffix(word, "s") {
		add(word + "s")
	}
	if !strings.HasSuffix(word, "e") && !strings.HasSuffix(word, "ing") && !strings.HasSuffix(word, "ed") {
		add(word + "ing")
		add(word + "ed")
		// commit -> committing, committed. Forms no transcript holds fall out
		// at the catalog.
		for _, form := range doubledForms(word) {
			add(form)
		}
	}
	return out
}

// undoubled is base without the consonant English doubles before -ing and -ed
// (logg -> log, pinn -> pin). Not -er: logger is log's, but letter, matter and
// summer are not let's, mat's and sum's, and the spelling cannot tell them apart, or "" when base does not end in one. Asked
// about "structured logging", recall missed the session that only says "log"
// and "logs": logging stripped to logg and stopped there (#4785).
func undoubled(base string) string {
	n := len(base)
	if n < 4 || base[n-1] != base[n-2] || !isConsonant(base[n-1]) {
		return ""
	}
	return base[:n-1]
}

// doubledForms is the other direction for a word ending
// consonant-vowel-consonant: log -> logging, logged.
func doubledForms(word string) []string {
	n := len(word)
	if n < 3 || !isConsonant(word[n-1]) || isConsonant(word[n-2]) || !isConsonant(word[n-3]) || strings.ContainsRune("wxy", rune(word[n-1])) {
		return nil
	}
	d := word + word[n-1:]
	return []string{d + "ing", d + "ed"}
}

func isConsonant(c byte) bool {
	return c >= 'a' && c <= 'z' && !strings.ContainsRune("aeiou", rune(c))
}

// anyNonASCII reports whether any term could carry or shed a combining mark.
func anyNonASCII(terms []string) bool {
	for _, term := range terms {
		if !isASCIIString(term) {
			return true
		}
	}
	return false
}

func hasFuzzyToken(terms []string, idx *tokenIndex) bool {
	for _, term := range terms {
		// The 4-rune floor also keeps CJK bigrams (always 2 runes) out of
		// fuzzy variant generation entirely — no variant-space blowup (#338).
		if len([]rune(term)) >= 4 {
			return true
		}
		// A shorter word is let in only to reach its own marked form: Arabic
		// writes its vowels as combining marks, so كتب is three runes and the
		// written-out كَتَبَ is six — the word a reader types is below the floor
		// while the word in the index is above it (#1941). The question is
		// asked of the term itself, not of the corpus: one Arabic word in one
		// session must not open the tier for every three-letter word a reader
		// types.
		if idx != nil && len(idx.markedFormsOf(term)) > 0 {
			return true
		}
	}
	return false
}

func tokenCatalog(dir string) (map[string]bool, error) {
	catalog, _, err := tokenCatalogDirs(dir)
	return catalog, err
}

// bucketDir is one bucket file's directory as the catalog read it, with the
// file as stat saw it then, so a reader can tell the file it opens later is
// still that one.
type bucketDir struct {
	path    string
	stat    os.FileInfo
	entries []bucketEntry
}

// tokenCatalogDirs is tokenCatalog that also keeps each bucket's directory.
func tokenCatalogDirs(dir string) (map[string]bool, []bucketDir, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil, nil
		}
		return nil, nil, err
	}
	var dirs []bucketDir
	tokens := 0
	for _, de := range entries {
		// An overlay holds tokens too, some of them in no bucket yet.
		if de.IsDir() || !isPostingsFile(de.Name()) {
			continue
		}
		path := filepath.Join(dir, "buckets", de.Name())
		header, f, err := openBucketDir(path)
		if err != nil {
			return nil, nil, err
		}
		fi, serr := f.Stat()
		_ = f.Close()
		if serr != nil {
			return nil, nil, serr
		}
		tokens += len(header)
		dirs = append(dirs, bucketDir{path: path, stat: fi, entries: header})
	}
	// Sized before it is filled: grown one token at a time, the map was
	// rehashed its way up to every token in the store, a third of what a
	// command-line search that reaches the catalog spent on it (#5386).
	catalog := make(map[string]bool, tokens)
	for _, d := range dirs {
		for _, entry := range d.entries {
			catalog[strings.TrimPrefix(entry.tok, "t")] = true
		}
	}
	return catalog, dirs, nil
}

func closeTokens(query string, idx *tokenIndex) []string {
	type match struct {
		token    string
		distance int
	}
	var matches []match
	qr := len([]rune(query))
	limit := 1
	if qr >= 8 {
		limit = 2
	}
	seen := make(map[string]bool)
	consider := func(token string, d int) {
		if seen[token] || d > limit {
			return
		}
		seen[token] = true
		matches = append(matches, match{token: token, distance: d})
	}
	// An edit changes the length by at most one and a transposition not at
	// all, so only tokens within the limit of the query's length can match.
	// Walking those buckets replaces a Damerau-Levenshtein run against every
	// token in the corpus — ~200k of them — with a few short slices.
	idx.candidates(qr, limit, func(token string) {
		if seen[token] {
			return
		}
		consider(token, damerauDistance(query, token, limit))
	})
	// And the same word written with its marks, or without them. Arabic is
	// normally typed without harakat and each mark is a rune of its own, so
	// كتب sat three edits from كَتَبَ while café sat one from cafe: one rule,
	// two outcomes, and the reader who got nothing was the one whose script
	// writes its vowels as marks (#1941).
	//
	// A lookup, not a walk: the marked forms of a word are the tokens that
	// strip to it, which is a map, and a walk over every marked token cost 36
	// ms a term on a corpus where most tokens carry marks. It also keeps the
	// rule honest — a mark is free, and nothing else is, so ข้าว is reachable
	// from ขาว but neither is reachable from a third word.
	//
	// Scored as one edit rather than none, because in Thai and Hebrew the mark
	// is what tells two words apart: a real typo correction must not sort
	// behind a word that merely lost its vowels, and the eight-variant cap
	// below must not fill with them.
	for _, token := range idx.markedFormsOf(query) {
		consider(token, 1)
	}
	// The same for ä, ö, ü and ß against their ASCII spellings (#4690).
	for _, token := range idx.digraphFormsOf(query) {
		consider(token, 1)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distance == matches[j].distance {
			return matches[i].token < matches[j].token
		}
		return matches[i].distance < matches[j].distance
	})
	if len(matches) > 8 {
		matches = matches[:8]
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.token
	}
	return out
}

// isASCIIString reports whether every byte is one rune, so the byte length is
// the rune count.
func isASCIIString(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func damerauDistance(a, b string, limit int) int {
	if len(a) <= 64 && len(b) <= 64 && utf8.ValidString(a) && utf8.ValidString(b) && utf8.RuneCountInString(a) == len(a) && utf8.RuneCountInString(b) == len(b) {
		var prev, prevPrev, cur [65]int
		for j := 0; j <= len(b); j++ {
			prev[j] = j
		}
		for i := 1; i <= len(a); i++ {
			cur[0] = i
			for j := 1; j <= len(b); j++ {
				cost := 0
				if a[i-1] != b[j-1] {
					cost = 1
				}
				cur[j] = min(cur[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
				if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
					cur[j] = min(cur[j], prevPrev[j-2]+1) //nolint:gosec // j <= len(b) <= 64 and the arrays hold 65
				}
			}
			prevPrev, prev, cur = prev, cur, prevPrev
		}
		return prev[len(b)]
	}
	return damerauDistanceRunes(a, b, limit)
}

func damerauDistanceRunes(a, b string, limit int) int {
	// Before the conversion: an edit changes the length by one, so a pair too
	// far apart in runes cannot match however it is spelled, and counting
	// costs no allocation.
	if abs(utf8.RuneCountInString(a)-utf8.RuneCountInString(b)) > limit {
		return limit + 1
	}
	// On the stack for a word of ordinary length. The close tier runs this
	// against every catalog token within two runes of the query's length, and
	// for a Cyrillic word that is tens of thousands of them: a slice per row
	// was most of what a Russian close-spelling step cost (#5378).
	var abuf, bbuf [maxIndexedTokenLen + 1]rune
	var rows [3][maxIndexedTokenLen + 2]int
	ar, br := runesInto(abuf[:0], a), runesInto(bbuf[:0], b)
	var prev, prevPrev, cur []int
	if len(br)+1 <= len(rows[0]) {
		prev, prevPrev, cur = rows[0][:len(br)+1], rows[1][:len(br)+1], rows[2][:len(br)+1]
	} else {
		prev, prevPrev, cur = make([]int, len(br)+1), make([]int, len(br)+1), make([]int, len(br)+1)
	}
	for j := range prev {
		prev[j] = j
	}
	copy(prevPrev, prev)
	prevMin := 0
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		rowMin := i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			cur[j] = min(cur[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				cur[j] = min(cur[j], prevPrev[j-2]+1)
			}
			rowMin = min(rowMin, cur[j])
		}
		// Every cell is built from the two rows above it, each step adding
		// nothing or one, so once two rows in a row are past max no later
		// row comes back under it. The callers only ask whether a token is
		// within max, and past it any number above max answers that.
		if rowMin > limit && prevMin > limit {
			return limit + 1
		}
		prevMin = rowMin
		prevPrev, prev, cur = prev, cur, prevPrev
	}
	return prev[len(br)]
}

// runesInto appends s's runes to buf, which is how a short word is decoded
// without allocating.
func runesInto(buf []rune, s string) []rune {
	for _, r := range s {
		buf = append(buf, r)
	}
	return buf
}

// isMark reports whether r is a combining mark: Mn covers Arabic harakat,
// Hebrew niqqud, Thai vowel signs and the Greek and Vietnamese tone marks in
// their decomposed form, and Mc the spacing matras of the Indic scripts, which
// are as much part of their word as any of those. The range test in front
// keeps ordinary text to one comparison per rune — every mark sits above
// U+0300.
func isMark(r rune) bool {
	if r < 0x0300 || isVariationSelector(r) {
		return false
	}
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

// isVariationSelector reports whether r only says how to draw the rune in
// front of it. They are category Mn, but gluing one to a word made "widget️"
// — the word plus an emoji presentation selector — a different token from
// "widget", which demoted an ordinary query off the exact tier.
func isVariationSelector(r rune) bool {
	return (r >= 0xFE00 && r <= 0xFE0F) || (r >= 0xE0100 && r <= 0xE01EF)
}

// hasMark reports whether s carries a combining mark, without building the
// stripped form: the index builder asks this of every non-ASCII token it sees
// and only strips the ones that answer yes.
func hasMark(s string) bool {
	for _, r := range s {
		if isMark(r) {
			return true
		}
	}
	return false
}

// unmarked strips combining marks from s, reporting whether any were there. A
// string without one is returned untouched, so ordinary text pays one scan and
// no allocation.
func unmarked(s string) (string, bool) {
	if !hasMark(s) {
		return s, false
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if isMark(r) {
			continue
		}
		out = append(out, r)
	}
	return string(out), true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
