package index

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
//
// An append commits its manifest and then retires the overlays it folded in.
// A reader that listed the directory before the retirement then opens an
// overlay that is gone. The manifest did not change, so the generation retry
// above never sees it, and recall failed with ENOENT until the next query
// (#6168). The whole read is tried once more from a fresh listing: an entry of
// the old listing is not skipped, because a file still missing on the second
// listing is a lost overlay, and that error is returned.
func tokenCatalogDirs(dir string) (map[string]bool, []bucketDir, error) {
	catalog, dirs, err := readTokenCatalogDirs(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return readTokenCatalogDirs(dir)
	}
	return catalog, dirs, err
}

// catalogListed, when set by a test, runs between the listing of the buckets
// directory and the open of its entries: where an append's cleanup lands.
var catalogListed func()

func readTokenCatalogDirs(dir string) (map[string]bool, []bucketDir, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil, nil
		}
		return nil, nil, err
	}
	if catalogListed != nil {
		catalogListed()
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
