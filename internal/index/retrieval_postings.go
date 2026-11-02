package index

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
)

func postingsFor(dir, tok string) ([]posting, error) {
	p, ok := bucketPath(dir, tok)
	if !ok {
		return nil, nil
	}
	return readBucketToken(p, tok, deadRecordsOf(dir))
}

// bucketName is every name bucket can return. A query reaches a file name
// through it, and saying so here is what lets a path checker see that.
var bucketName = regexp.MustCompile(`^(x[0-9a-f]{2}|[a-z0-9_]{2})$`)

func bucketPath(dir, tok string) (string, bool) {
	b := bucket(tok)
	if !bucketName.MatchString(b) {
		return "", false
	}
	return filepath.Join(dir, "buckets", b+".bin"), true
}

// OtherWordForms lists, per query term, the other forms of that word the
// corpus actually holds — "retries" for "retry", "rotated" for "rotate".
//
// The stem tier already knows these, but it only ever runs after the exact
// tier came up empty. When the exact tier does answer, `retry` returns the
// sessions that wrote "retry" and stops: the sessions that wrote "retries"
// are neither returned nor mentioned, and the ladder is invisible from the
// outside. This is the lookup that lets the caller say so.
func OtherWordForms(dir string, terms []string) map[string][]string {
	if dir == "" {
		dir = DefaultDir()
	}
	catalog, err := tokenCatalogCached(dir)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, term := range terms {
		var others []string
		for _, form := range stemMatches(term, catalog) {
			if form != term {
				others = append(others, form)
			}
		}
		if len(others) > 0 {
			out[term] = others
		}
	}
	return out
}

// TermSessionCounts says how many sessions each term appears in on its own.
// When an AND query finds no intersection, "try fewer words" is right in
// direction and empty in content — the reader has to guess which of their words
// to drop, while deja already read these counts to decide there was none (#826).
func TermSessionCounts(dir string, terms []string) map[string]int {
	return termSessionCounts(dir, terms, false)
}

// TermSessionCountsSpoken counts only the sessions where a term appears in
// something said rather than in something a tool printed.
//
// The caller is the word-forms note, and the difference is what that note is
// for. deja prints the note into a terminal; a session that ran deja keeps
// what it printed; the next index reads that transcript. So a form deja
// generated to search for — "pgbouncereds", "retriesed" — comes back as a form
// the store holds, and the note names it as a word somebody wrote. Measured on
// a 2,739-session store: every generated form sat in one or two sessions and in
// zero spoken ones, while the rarest real form was in three spoken sessions
// (#3820). The tool bit already rides in the posting, so this costs no extra
// read.
func TermSessionCountsSpoken(dir string, terms []string) map[string]int {
	return termSessionCounts(dir, terms, true)
}

func termSessionCounts(dir string, terms []string, spokenOnly bool) map[string]int {
	if dir == "" {
		dir = DefaultDir()
	}
	// Counted over what search would serve. Postings hold every session, the
	// answer holds none the ignore rule covers, and counting the two
	// differently had deja say "no session has them together" about a phrase
	// two withheld sessions carry word for word (#2562).
	servable := servableSids(dir)
	out := make(map[string]int, len(terms))
	for _, t := range terms {
		key := "t" + t
		posts, err := postingsFor(dir, key)
		if err != nil {
			// Best effort by design: this only decorates the "try fewer words"
			// hint after a search that already returned. A term whose bucket
			// will not read drops out of the advice rather than failing the
			// command — a real corruption already surfaced on the search path
			// that ran first.
			continue
		}
		seen := map[uint32]bool{}
		for _, p := range posts {
			if servable != nil && !servable[p.Sid] {
				continue
			}
			if spokenOnly && p.Tool {
				continue
			}
			seen[p.Sid] = true
		}
		out[t] = len(seen)
	}
	return out
}

// servableSids is the set of session ordinals the ignore rule leaves in reach.
// nil when the manifest cannot be read, which the callers treat as "count
// everything" rather than "count nothing".
func servableSids(dir string) map[uint32]bool {
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	excluded := excludedHarnesses()
	out := make(map[uint32]bool, len(m.Sessions))
	for _, meta := range m.Sessions {
		if !metaIgnoredOrExcluded(pol, excluded, meta) {
			out[meta.Ord] = true
		}
	}
	return out
}

// ignoredSids is the set of session ordinals withheld by the ignore rule (and
// not from an excluded harness).
func ignoredSids(dir string) map[uint32]bool {
	m, err := readManifestCached(dir)
	if err != nil {
		return nil
	}
	pol := policy.Load()
	excluded := excludedHarnesses()
	out := make(map[uint32]bool)
	for _, meta := range m.Sessions {
		if !excluded[strings.ToLower(meta.Harness)] && pol.Ignored(meta.Path, meta.Project) {
			out[meta.Ord] = true
		}
	}
	return out
}

// IgnoredWithAllTerms counts the sessions that hold every one of these terms
// and that the ignore rule keeps out of the answer. The listing says what the
// rule withheld from it (#2554); a search that comes back short or empty owes
// the reader the same sentence (#2562).
func IgnoredWithAllTerms(dir string, terms []string) int {
	if dir == "" {
		dir = DefaultDir()
	}
	if len(terms) == 0 {
		return 0
	}
	ignored := ignoredSids(dir)
	if len(ignored) == 0 {
		return 0
	}
	// Each term matches any of its forms, the way the tiers match. Counting the
	// exact spelling alone left the line silent for a reader who typed "pool"
	// while the withheld session wrote "pools" — the moment it is most needed,
	// since search then serves nothing and the rule is why (#2573).
	forms := OtherWordForms(dir, terms)
	var per []map[uint32]bool
	for _, term := range terms {
		hit := map[uint32]bool{}
		for _, form := range append([]string{term}, forms[term]...) {
			posts, err := postingsFor(dir, "t"+form)
			if err != nil {
				continue
			}
			for _, p := range posts {
				if ignored[p.Sid] {
					hit[p.Sid] = true
				}
			}
		}
		if len(hit) == 0 {
			return 0
		}
		per = append(per, hit)
	}
	if len(per) == 0 {
		return 0
	}
	n := 0
	for sid := range per[0] {
		all := true
		for _, hit := range per[1:] {
			if !hit[sid] {
				all = false
				break
			}
		}
		if all {
			n++
		}
	}
	return n
}

func intersectPostings(dir string, keys []string) ([]posting, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	lists := make([][]posting, 0, len(keys))
	for _, key := range keys {
		list, err := postingsFor(dir, key)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, nil
		}
		lists = append(lists, list)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	set := make(map[int64]posting, len(lists[0]))
	for _, p := range lists[0] {
		set[p.Off] = p
	}
	for _, list := range lists[1:] {
		next := make(map[int64]posting, min(len(set), len(list)))
		for _, p := range list {
			if _, ok := set[p.Off]; ok {
				next[p.Off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out, nil
}

func intersectSubstringPostings(dir string, bare []string) ([]posting, error) {
	posts, _, err := intersectSubstringPostingsDetailed(dir, bare)
	return posts, err
}

// compoundQueryTokens spells a compound query token as its parts, and says how
// the words read spelled apart.
//
// The index already splits what it stores — indexKeys adds identifierParts,
// which is why `retry backoff` reaches a session that wrote `retry-backoff`.
// The other direction had nothing: a store that says "blowing up" was
// unreachable from `blowing-up`, because this rung expands a query token to
// indexed tokens *containing* it ("code" finds "opencode") and no indexed token
// contains a compound the store never wrote (#2125).
//
// The parts replace the compound rather than joining it: the list is
// intersected, so a token that expands to nothing empties the result. A query
// naming a compound the store really holds never arrives here — the exact tier
// answered it — so the replacement costs nothing that works.
func compoundQueryTokens(toks []string) ([]string, map[string]string) {
	out := make([]string, 0, len(toks))
	apart := map[string]string{}
	for _, tok := range toks {
		if !strings.ContainsAny(tok, "-_") {
			out = append(out, tok)
			continue
		}
		var parts []string
		for _, part := range strings.FieldsFunc(tok, func(r rune) bool { return r == '-' || r == '_' }) {
			if len(part) >= 2 {
				parts = append(parts, part)
			}
		}
		if len(parts) < 2 {
			out = append(out, tok)
			continue
		}
		out = append(out, parts...)
		apart[tok] = strings.Join(parts, " ")
	}
	return out, apart
}

func intersectSubstringPostingsDetailed(dir string, bare []string) ([]posting, map[string][]string, error) {
	if len(bare) == 0 {
		return nil, nil, nil
	}
	if len(bare) > 3 {
		bare = bare[:3] // the first three as typed; keep the expansion bounded
	}
	// From the directories the catalog already holds. Reading all of them off
	// disk again, every bucket of the store, was most of what this step cost,
	// and it runs whenever the words never meet as typed, which is most
	// Russian questions (#5378).
	if idx, err := tokenIndexCached(dir); err == nil && idx.dirs != nil {
		if posts, variants, ok, err := substringPostingsFrom(idx.dirs, bare, deadRecordsOf(dir)); ok || err != nil {
			return posts, variants, err
		}
	}
	buckets, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var dirs []bucketDir
	for _, de := range buckets {
		if de.IsDir() || !isPostingsFile(de.Name()) {
			continue
		}
		dirs = append(dirs, bucketDir{path: filepath.Join(dir, "buckets", de.Name())})
	}
	posts, variants, _, err := substringPostingsFrom(dirs, bare, deadRecordsOf(dir))
	return posts, variants, err
}

// substringPostingsFrom intersects, word by word, the postings of every token
// that contains the word. A bucket with entries is read through them, and ok
// is false when its file is no longer the one they were read from, so the
// caller reads the store as it is now; one without is read off disk.
// An overlay is one more file of postings; the dead records' are left out.
func substringPostingsFrom(dirs []bucketDir, bare []string, dead []int64) (_ []posting, _ map[string][]string, ok bool, _ error) {
	perTok := make([]map[int64]posting, len(bare))
	variants := make(map[string][]string, len(bare))
	for i := range perTok {
		perTok[i] = map[int64]posting{}
	}
	for _, bd := range dirs {
		entries := bd.entries
		var f *os.File
		if entries == nil {
			var err error
			entries, f, err = openBucketDir(bd.path)
			if err != nil {
				// The directory listing named this bucket, so a miss now means
				// a concurrent rebuild swapped it out — tolerate that. Anything
				// else (a corrupt header, a denied read) is real and is
				// surfaced so the fuzzy tier does not silently under-match a
				// damaged store.
				if os.IsNotExist(err) {
					continue
				}
				return nil, nil, false, err
			}
		}
		for _, e := range entries {
			tok := strings.TrimPrefix(e.tok, "t")
			for i, b := range bare {
				if !strings.Contains(tok, b) {
					continue
				}
				if f == nil {
					var err error
					if f, err = openIndexFile(bd.path); err != nil {
						return nil, nil, false, nil //nolint:nilerr // not ok: the caller reads the slow way
					}
					// The same file, not one of the same size: a rebuild renames a
					// new bucket in, and a coarse clock can give it the old time.
					if fi, err := f.Stat(); err != nil || !os.SameFile(fi, bd.stat) || fi.Size() != bd.stat.Size() || !fi.ModTime().Equal(bd.stat.ModTime()) {
						_ = f.Close()
						return nil, nil, false, nil //nolint:nilerr // not ok: the caller reads the slow way
					}
				}
				// Once, though an overlay can hold the token beside its bucket.
				if !slices.Contains(variants[b], tok) {
					variants[b] = append(variants[b], tok)
				}
				buf := make([]byte, e.n)
				if _, err := f.ReadAt(buf, int64(e.off)); err != nil { //nolint:gosec // offsets are checked against the bucket size on open
					// The bucket directory validated but the block it points to
					// will not read: corruption, not a missing token. Surface it.
					_ = f.Close()
					return nil, nil, false, fmt.Errorf("%w: %w", errCorruptIndex, err)
				}
				posts, err := decodePostingBlock(buf)
				if err != nil {
					_ = f.Close()
					return nil, nil, false, err
				}
				for _, p := range posts {
					perTok[i][p.Off] = p
				}
			}
		}
		if f != nil {
			_ = f.Close()
		}
	}
	set := perTok[0]
	for _, m := range perTok[1:] {
		next := map[int64]posting{}
		for off, p := range m {
			if _, ok := set[off]; ok {
				next[off] = p
			}
		}
		set = next
		if len(set) == 0 {
			return nil, nil, true, nil
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	out = liveOnly(out, dead)
	for token := range variants {
		sort.Strings(variants[token])
	}
	return out, variants, true, nil
}

// commonTokenPostings is where a word stops being worth widening. Below it a
// near-neighbour is usually the same thing spelled differently and is worth
// the candidate walk; at or above it the word is ordinary vocabulary the
// corpus is full of, and its neighbours only cost time.
const commonTokenPostings = 200

func intersectPostingMaps(sets []map[int64]posting) []posting {
	if len(sets) == 0 {
		return nil
	}
	set := sets[0]
	for _, next := range sets[1:] {
		for off := range set {
			if _, ok := next[off]; !ok {
				delete(set, off)
			}
		}
	}
	out := make([]posting, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out
}

// tokenHitCap bounds the buffer one token is built in. tokens flushes a token
// once it passes 64 bytes, so nothing longer than that plus one rune is ever
// held here.
const tokenHitCap = 64 + utf8.UTFMax

// tokenHits reports which of the wanted tokens a text contains.
//
// It tokenises exactly as tokens does — same fold, same rune classes, same
// two-byte minimum and same flush past 64 bytes — and then throws the token
// away instead of collecting it. The ranker asks whether a message carries any
// of a query's five or ten words, and building the message's whole token set to
// answer that was 25% of the CPU a recall answer spent: 4000 turns of each of
// fifty sessions, a map and a sort per turn.
func tokenHits(s string, want map[string]bool) map[string]bool {
	if len(want) == 0 {
		return nil
	}
	s = nfcfold.Compose(s)
	var hit map[string]bool
	var buf [tokenHitCap]byte
	n := 0
	flush := func() {
		if n >= 2 {
			// The compiler does not allocate for a map lookup keyed by a
			// converted byte slice, which is the point of the buffer.
			if want[string(buf[:n])] {
				if hit == nil {
					hit = make(map[string]bool, len(want))
				}
				hit[string(buf[:n])] = true
			}
		}
		n = 0
	}
	for _, r := range query.Lower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' ||
			(n > 0 && isMark(r)) {
			n += utf8.EncodeRune(buf[n:], r)
			if n > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flush()
	return hit
}

// tokens is the distinct words of s, longest first, as the query side and the
// sidecars read them. eachWord cuts them, the same walk the index keys with.
func tokens(s string) []string {
	seen := map[string]bool{}
	var out []string
	var buf [1 + tokenHitCap]byte
	eachWord(s, &buf, func(b []byte) {
		// A map lookup keyed by a converted byte slice does not allocate, so
		// only a word not seen yet is copied.
		if seen[string(b[1:])] {
			return
		}
		t := string(b[1:])
		seen[t] = true
		out = append(out, t)
	})
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// indexKeys collects what textKeys emits, in order and with its repeats. The
// build path streams the keys instead; this is what the tests read, so they
// read the same emitter the build does.
//
// Bigram keys fold Traditional to Simplified so the same word written either
// way lands on one key and a query in one script reaches content in the other.
// Folding only the key keeps the stored text untouched. The emitter folds runs
// in place and dedupes folded pairs — cheaper than folding each emitted
// bigram, and every caller collapses repeated keys anyway (#492).
func indexKeys(s string) []string {
	var out []string
	textKeys(s, func(tok string) {
		out = append(out, tok)
	})
	return out
}

// identifierParts is what keyer.identifierParts emits, as words.
func identifierParts(s string) []string {
	var out []string
	var k keyer
	k.identifierParts(s, func(b []byte) { out = append(out, string(b[1:])) })
	return out
}

func splitCompound(word []rune, out *[]string) {
	var k keyer
	k.splitCompound(word, func(b []byte) { *out = append(*out, string(b[1:])) })
}

func retrievalKeys(keys []string) []string {
	// Fetch postings for up to 8 tokens: a bucket read is sub-millisecond and
	// intersectPostings sorts the fetched lists rarest-first with an early
	// exit, so more keys means a more selective AND, not a slower one. The old
	// cap of 3 longest tokens guessed at rarity by length and guessed wrong on
	// long-but-common words.
	if len(keys) <= 8 {
		return keys
	}
	return keys[:8]
}

func queryKeys(s string) []string {
	toks := query.ExpandCJKTokens(tokens(s))
	if len(toks) == 0 {
		return nil
	}
	// Drop stop words so retrievalKeys picks selective content tokens; a
	// long stop word like "before" must not over-constrain the AND. If the
	// query is all stop words, keep them (odd results beat none).
	content := make([]string, 0, len(toks))
	for _, tok := range toks {
		// A bigram of two function runes is grammar, and the index no longer
		// stores one: asking for it would AND the query against a key that
		// cannot exist (#492).
		if !query.IsStopWord(tok) && !query.CJKFunctionBigram(tok) {
			content = append(content, tok)
		}
	}
	if len(content) == 0 {
		content = toks
	}
	out := make([]string, 0, len(content))
	for _, tok := range content {
		// Mirror of indexKeys: the posting side is folded, so the query side
		// must be too.
		out = append(out, "t"+cjkfold.String(tok))
	}
	return out
}

func recordMatchesQuery(r Record, o query.Options) bool {
	return recordMatchesQueryVariants(r, o, nil)
}

func recordMatchesQueryVariants(r Record, o query.Options, variants map[string][]string) bool {
	return newRecordMatcher(o, variants).matches(r)
}

// recordMatcher is the query side of the verification check, built once.
//
// The check runs per record — for a query with no postings, that is every
// record in the store — and everything derived from the query is the same
// every time: the parts, the composed form, the CJK-folded form and their
// parts. Rebuilding them per record doubled the cost of a decomposed query
// (4.1 ms against 1.9 ms over 120 sessions, and 49385 allocations against
// 22984), since such a query takes the retry path on every record (#1885).
type recordMatcher struct {
	regex    bool
	matchAll bool
	variants map[string][]string

	terms, phrases []string

	// nfc is the query composed, kept apart so a record can be tested against
	// it without composing the query again.
	nfc        string
	nfcDiffers bool
	nfcTerms   []string
	nfcPhrases []string
	cjk        string
	cjkDiffers bool
	cjkTerms   []string
	cjkPhrases []string
}

func newRecordMatcher(o query.Options, variants map[string][]string) recordMatcher {
	m := recordMatcher{regex: o.Regex, variants: variants}
	if o.Regex {
		return m
	}
	m.terms, m.phrases = query.QueryParts(o.Query)
	if len(m.terms) == 0 && len(m.phrases) == 0 {
		m.matchAll = true
		return m
	}
	m.nfc = nfcfold.Compose(o.Query)
	m.nfcDiffers = m.nfc != o.Query
	m.nfcTerms, m.nfcPhrases = query.QueryParts(m.nfc)
	m.cjk = cjkfold.String(o.Query)
	m.cjkDiffers = m.cjk != o.Query
	m.cjkTerms, m.cjkPhrases = query.QueryParts(m.cjk)
	return m
}

func (m recordMatcher) matches(r Record) bool {
	if m.regex || m.matchAll {
		return true
	}
	if query.MatchesParts(r.Text, m.terms, m.phrases, m.variants) {
		return true
	}
	// The postings that pointed here folded NFD to NFC (tokens); this surface
	// check runs on the raw bytes, where the same accented word in the other
	// normalization would not substring-match. Retry both composed, mirroring
	// the CJK retry below (#1098).
	if composed := nfcfold.Compose(r.Text); m.nfcDiffers || composed != r.Text {
		if query.MatchesParts(composed, m.nfcTerms, m.nfcPhrases, m.variants) {
			return true
		}
	}
	// Postings are keyed on Traditional-folded bigrams, so a Simplified query
	// can legitimately reach a Traditional record (and the reverse). This
	// verification step compares surface text, which would then reject the
	// very record the postings pointed at — retry it on both sides folded.
	folded := cjkfold.String(r.Text)
	if folded == r.Text && !m.cjkDiffers {
		return false
	}
	return query.MatchesParts(folded, m.cjkTerms, m.cjkPhrases, m.variants)
}

// bucket shards a token by its opening characters. It used to slice two
// *bytes*, which for any non-ASCII token is a prefix plus half a UTF-8
// sequence: safe() mapped the broken half to "_", so every Russian, Chinese
// and Greek token in the corpus landed in the single bucket "t_". Each lookup
// then had to scan the whole non-ASCII vocabulary. Sharding by runes keeps
// ASCII bucket names exactly as they were and spreads the rest over 256.
// isShardASCII reports whether a rune is one byte wide, so slicing it cannot
// split a multi-byte sequence.
func isShardASCII(r rune) bool { return r < 128 }

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

// dfMessageCap is how much one session may contribute to a term's document
// frequency. Counting whole sessions inverts the signal on a real store: eleven
// sessions of twenty to sixty thousand messages hold 99% of everything ever
// said, so a subject word living in sixteen of them scores 1.31 while "branch",
// living in thirteen, scores 1.50 — the most specific word in the store ranks
// as commoner than the most ordinary one. Counting every message instead brings
// back the failure this deliberately avoids, where one marathon repeating a
// word three hundred times makes it common.
//
// Capping the contribution keeps both. Measured on a real store, a cap of 25
// reproduces what treating each 200-message episode as a document gives —
// pgbouncer 3.40 against 3.37, singbox 4.17 against 4.70 — while working words
// stay near 2: branch 2.18, suite 2.53, windows 2.15. The separation lands on
// the strong floor that was already there.
const dfMessageCap = 25

// countedDocs is the corpus size in the unit noteDF counts: messages, with each
// session contributing at most dfMessageCap of them. A manifest written before
// message counts were kept degrades to one document per session, which is what
// the ranking did before — the numbers stay consistent, they just stay coarse
// until the index is next built.
func countedDocs(m Manifest) int {
	return viewsFor(m).countedDocs()
}

// stemMatchForms generates the same candidate surface forms stemMatches
// derives, without requiring the token catalog: absent forms simply read
// empty buckets.
func stemMatchForms(term string) []string {
	runes := []rune(term)
	// Cyrillic terms took the ASCII path, which appends English suffixes to
	// Russian words — "миграции" became "миграцииed" and read nothing but
	// empty buckets, so the relevance-tier fold was a no-op in both
	// directions. Route them through the Russian ending table instead.
	if isCyrToken(term) {
		return cyrMatchForms(term)
	}
	if len(runes) < 5 {
		var out []string
		for _, form := range []string{term + "s", term + "es", strings.TrimSuffix(term, "s")} {
			if len(form) >= 3 && form != term {
				out = append(out, form)
			}
		}
		return out
	}
	var out []string
	for _, form := range suffixForms(term) {
		if form != term {
			out = append(out, form)
		}
	}
	return out
}

// isCyrToken reports whether the token is written in Cyrillic.
func isCyrToken(term string) bool {
	for _, r := range term {
		if r >= 0x400 && r <= 0x4FF {
			return true
		}
		if r < 128 {
			return false
		}
	}
	return false
}

// askedMinSpan is how far apart two askings must be to mean anything.
const askedMinSpan = 48 * time.Hour

// AskedTwice is one question this store has been asked in more than one
// session, with the sessions that asked it, newest first. Empty when nothing
// repeats — which is the honest answer for a store a few days old.
type AskedTwice struct {
	Text     string
	Sessions []SessionMeta
}

// FindAskedTwice picks the question worth showing: the one asked in the most
// sessions, and among equals the one spanning the longest stretch of time. A
// thing asked twice in one afternoon is a person working; the same thing asked
// in March and again in July is the thing this tool exists for.
//
// The search runs entirely over the manifest. Only the matching sessions are
// read back, and only to recover the text the hashes stand for.
// allow is the trust gate: it reports whether a session's project may reach the
// caller's activation. Imported sessions now carry asked hashes, so an
// asked-twice repeat can be all imported — without this gate the brief would
// print it on a machine whose auto rule withholds imported memory. A nil allow
// counts every session (the manifest-level view the tests read).
func FindAskedTwice(dir string, allow func(project string) bool) (AskedTwice, bool) {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	if err != nil {
		return AskedTwice{}, false
	}
	byHash := map[uint64][]SessionMeta{}
	for _, meta := range m.Sessions {
		if allow != nil && !allow(meta.Project) {
			continue
		}
		for _, h := range meta.Asked {
			byHash[h] = append(byHash[h], meta)
		}
	}
	var best []SessionMeta
	var bestSpan time.Duration
	var bestHash uint64
	for h, metas := range byHash {
		if len(metas) < 2 {
			continue
		}
		sort.Slice(metas, func(i, j int) bool { return newestFirstMeta(metas[i], metas[j]) })
		metas = oneConversationEach(metas, h)
		if len(metas) < 2 {
			continue
		}
		span := metas[0].Updated.Sub(metas[len(metas)-1].Updated)
		// Time between the askings, not the number of them. The same question
		// sixteen times in one afternoon is somebody retrying; the same question
		// in March and again in July is the thing worth showing. Below a couple
		// of days it is the former.
		if span < askedMinSpan {
			continue
		}
		if span > bestSpan || (span == bestSpan && len(metas) > len(best)) {
			text := askedTextFor(dir, m, metas, h)
			if text == "" || notAsked(text) || !looksLikeQuestion(text) {
				continue
			}
			best, bestSpan, bestHash = metas, span, h
		}
	}
	if len(best) < 2 {
		return AskedTwice{}, false
	}
	text := askedTextFor(dir, m, best, bestHash)
	if text == "" {
		return AskedTwice{}, false
	}
	return AskedTwice{Text: text, Sessions: best}, true
}

// oneConversationEach keeps one session per conversation. A resume, a fork or
// a share writes the same conversation under a second key, and both keys carry
// the same asked hashes — so "you asked this in two sessions" was the reader's
// own continued conversation, told back to them as a repeat. Measured on a
// 2,300-session store: all ten repeats at least 48 hours apart were one
// conversation under several keys, and the line had never had anything else to
// show (#3711).
//
// The signal is in the manifest, so this costs no record read: the two
// sessions' asked sets, minus the hash they matched on. Two sessions that share
// only the question itself are the case this surface exists for and must
// survive, which is why want is excluded rather than counted. A third of the
// rest in common is a copy; on that store the verdict was the same anywhere
// between a fifth and a half, and dropped nine of the ten.
func oneConversationEach(metas []SessionMeta, want uint64) []SessionMeta {
	var out []SessionMeta
	for _, meta := range metas {
		copyOf := false
		for _, kept := range out {
			if sameConversation(kept, meta, want) {
				copyOf = true
				break
			}
		}
		if !copyOf {
			out = append(out, meta)
		}
	}
	return out
}

// sameConversation reports whether two sessions look like one conversation
// written twice, judged by the asked hashes they hold besides want.
func sameConversation(a, b SessionMeta, want uint64) bool {
	in := map[uint64]bool{}
	for _, h := range a.Asked {
		if h != want {
			in[h] = true
		}
	}
	both, union := 0, len(in)
	for _, h := range b.Asked {
		if h == want {
			continue
		}
		if in[h] {
			both++
			continue
		}
		union++
	}
	if union == 0 {
		return false
	}
	return float64(both)/float64(union) >= askedCopyShare
}

// askedCopyShare is how much of two sessions' other questions must coincide
// before they are read as one conversation rather than two askings.
const askedCopyShare = 1.0 / 3.0

// askedTextFor recovers what a hash stood for by reading back the sessions that
// carry it, stopping at the first one that yields the question.
func askedTextFor(dir string, m Manifest, metas []SessionMeta, want uint64) string {
	for _, meta := range metas {
		s, ok, err := loadSessionMeta(dir, m, meta)
		if err != nil || !ok {
			continue
		}
		for _, msg := range s.Messages {
			if msg.Role != "user" {
				continue
			}
			stem := questionStem(msg.Text)
			if stem == "" {
				continue
			}
			h := fnv.New64a()
			_, _ = h.Write([]byte(stem))
			if h.Sum64() == want {
				return strings.TrimSpace(msg.Text)
			}
		}
	}
	return ""
}

// consonantY splits a word ending in consonant+y, the form whose plural is
// -ies rather than -s. "key" and "day" end in vowel+y and keep their -s.
func consonantY(word string) (string, bool) {
	if !strings.HasSuffix(word, "y") || len(word) < 3 {
		return "", false
	}
	switch word[len(word)-2] {
	case 'a', 'e', 'i', 'o', 'u':
		return "", false
	}
	return strings.TrimSuffix(word, "y"), true
}
