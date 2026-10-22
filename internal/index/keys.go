package index

import (
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
)

// keyer writes the posting keys of a text into buffers it reuses, and hands
// each key over as bytes that are only valid for the call. A cold build keys
// every message through it: built as strings, the keys were a string per token
// per message, a string builder per token, a lowered copy of each message and
// a map per message — half the objects a 12k-session build allocated, and
// enough garbage that the workers spent a fifth of their CPU spinning on the
// runtime's heap lock (#5567). A spill worker looks a key up by its bytes and
// copies it only the first time a batch sees it.
//
// It emits what tokens, identifierParts, cjkIndexKeys and dateTokens give,
// each prefixed with "t", in text order and with repeats; every caller
// collapses repeats itself.
type keyer struct {
	word  [1 + tokenHitCap]byte
	runes []rune
	part  []byte
	cjk   map[uint64]struct{}
	pair  [1 + 2*utf8.UTFMax]byte

	month int
	dates [3][]byte
}

// keys emits every key a message earns: its text's, then its date's.
func (k *keyer) keys(text string, when time.Time, emit func([]byte)) {
	k.textKeys(text, emit)
	k.dateKeys(when, emit)
}

func (k *keyer) textKeys(text string, emit func([]byte)) {
	eachWord(text, &k.word, emit)
	k.identifierParts(text, emit)
	k.cjkKeys(text, emit)
}

// eachWord streams the words of s as tokens cuts them, repeats included, each
// as "t" plus the word in buf. It folds and lowercases rune by rune, which is
// what lowering the whole text first gives: query.Lower maps each rune on its
// own, and an invalid byte decodes to U+FFFD either way.
func eachWord(s string, buf *[1 + tokenHitCap]byte, emit func([]byte)) {
	// Fold NFD to NFC so an accented word keys the same whether it was typed or
	// stored decomposed. A combining mark is category Mn, not a letter, so the
	// walk below would otherwise split "café" (NFD) into "cafe" and drop the
	// accent, keying it apart from the precomposed "café" (#1098). Both the
	// stored text and the query pass through here, so the fold is symmetric and
	// the two sides always meet on one key.
	s = nfcfold.Compose(s)
	buf[0] = 't'
	n := 1
	for _, r := range s {
		r = query.LowerRune(r)
		// A combining mark continues the word it sits on. Latin NFD is already
		// composed away above, but Arabic harakat, Hebrew niqqud, Thai vowel
		// signs and the Indic matras have no precomposed form, so the mark
		// ended the token: كَتَبَ was indexed as three one-letter tokens and
		// हिन्दी as two, and the words themselves were not in the index at all
		// (#1941). A mark cannot start a token, so a stray one still
		// separates.
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' ||
			(n > 1 && isMark(r)) {
			n += utf8.EncodeRune(buf[n:], r)
			if n <= 1+64 {
				continue
			}
		}
		// Two bytes is the shortest word. A word is cut once it passes 64.
		if n >= 1+2 {
			emit(buf[:n])
		}
		n = 1
	}
	if n >= 1+2 {
		emit(buf[:n])
	}
}

// identifierParts emits the lowered inner words of compound identifiers so
// `deja "user profile"` finds getUserProfile and refresh_token_rotation.
// It walks the original-cased text: case humps are gone after lowering.
// Only words of 6+ runes with a real boundary produce parts, and only parts
// of 3+ runes are kept — short fragments ride the substring tier instead.
func (k *keyer) identifierParts(s string, emit func([]byte)) {
	word := k.runes[:0]
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			word = append(word, r)
			if len(word) <= 64 {
				continue
			}
		}
		if len(word) >= 6 {
			k.splitCompound(word, emit)
		}
		word = word[:0]
	}
	if len(word) >= 6 {
		k.splitCompound(word, emit)
	}
	k.runes = word[:0]
}

func (k *keyer) splitCompound(word []rune, emit func([]byte)) {
	start := 0
	boundaries := 0
	part := func(end int) {
		if end-start >= 3 {
			b := append(k.part[:0], 't') //nolint:gocritic // reuses k.part's buffer
			for _, r := range word[start:end] {
				b = utf8.AppendRune(b, query.LowerRune(r))
			}
			k.part = b
			emit(b)
		}
		start = end
	}
	for i := 1; i < len(word); i++ {
		c, p := word[i], word[i-1]
		switch {
		case c == '_' || c == '-':
			part(i)
			start = i + 1
			boundaries++
		case unicode.IsUpper(c) && (unicode.IsLower(p) || unicode.IsDigit(p)):
			// getUser | getUserById: hump boundary
			part(i)
			boundaries++
		case unicode.IsLower(c) && unicode.IsUpper(p) && i-1 > start:
			// JSONData -> JSON | Data: break before the last upper
			part(i - 1)
			boundaries++
		}
	}
	if boundaries > 0 {
		part(len(word))
	}
}

// cjkKeys emits folded posting keys directly from CJK runs, a folded pair
// once per text. The packed representation widens each rune before shifting
// so supplementary-plane values cannot overflow (#492).
func (k *keyer) cjkKeys(s string, emit func([]byte)) {
	// A CJK rune is never a single UTF-8 byte, so this guard preserves the
	// cjkBigrams fast path without decoding an ASCII-only message.
	ascii := true
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return
	}
	if k.cjk == nil {
		k.cjk = map[uint64]struct{}{}
	}
	seen := k.cjk
	defer func() {
		// Clearing walks the whole table, so one long Han text would make every
		// later message pay for its size.
		if len(seen) > 1024 {
			k.cjk = nil
		} else {
			clear(seen)
		}
	}()
	emitFolded := func(r1, r2 rune) {
		// Grammar earns no posting. A pair of function runes — 的了, 在哪,
		// 什么 — is the CJK counterpart of a stop word: the query side has
		// dropped it from the term list since it was written, so the postings
		// were carrying the most frequent pairs in the language for nothing
		// (#492). queryKeys drops them from the AND for the same reason.
		if r1 != 0 && query.CJKFunctionRune(r1) && query.CJKFunctionRune(r2) {
			return
		}
		key := (uint64(r1) << 21) | uint64(r2) //nolint:gosec // runes are non-negative
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		k.pair[0] = 't'
		n := 1
		if r1 != 0 {
			n += utf8.EncodeRune(k.pair[n:], r1)
		}
		n += utf8.EncodeRune(k.pair[n:], r2)
		emit(k.pair[:n])
	}

	var previous rune
	runLength := 0
	flush := func() {
		if runLength == 1 {
			// Zero is only the packed-key marker for a unigram. It is never
			// written into the emitted token.
			emitFolded(0, previous)
		}
		runLength = 0
	}

	for _, r := range s {
		if !cjkfold.Unspaced(r) {
			flush()
			continue
		}

		r = cjkfold.Rune(r)
		if runLength == 0 {
			previous = r
			runLength = 1
			continue
		}

		emitFolded(previous, r)
		previous = r
		runLength = 2
	}
	flush()
}

// dateKeys emits dateTokens(when). A store is read a session at a time, so
// the next message is nearly always in the month of the last one, and the keys
// are built once per month rather than once per message.
func (k *keyer) dateKeys(when time.Time, emit func([]byte)) {
	if when.IsZero() {
		return
	}
	u := when.UTC()
	m := u.Year()*12 + int(u.Month())
	if k.dates[0] == nil || k.month != m {
		for i, tok := range dateTokens(when) {
			k.dates[i] = append(k.dates[i][:0], tok...)
		}
		k.month = m
	}
	for _, b := range k.dates {
		emit(b)
	}
}
