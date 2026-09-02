package index

import (
	"hash/fnv"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/query"
)

// hexBuckets fixes every shard name in a lookup table. bucket relies on these
// exact names for on-disk compatibility while avoiding formatter work in the
// build path addressed by #492.
var hexBuckets = [256]string{
	"x00", "x01", "x02", "x03", "x04", "x05", "x06", "x07",
	"x08", "x09", "x0a", "x0b", "x0c", "x0d", "x0e", "x0f",
	"x10", "x11", "x12", "x13", "x14", "x15", "x16", "x17",
	"x18", "x19", "x1a", "x1b", "x1c", "x1d", "x1e", "x1f",
	"x20", "x21", "x22", "x23", "x24", "x25", "x26", "x27",
	"x28", "x29", "x2a", "x2b", "x2c", "x2d", "x2e", "x2f",
	"x30", "x31", "x32", "x33", "x34", "x35", "x36", "x37",
	"x38", "x39", "x3a", "x3b", "x3c", "x3d", "x3e", "x3f",
	"x40", "x41", "x42", "x43", "x44", "x45", "x46", "x47",
	"x48", "x49", "x4a", "x4b", "x4c", "x4d", "x4e", "x4f",
	"x50", "x51", "x52", "x53", "x54", "x55", "x56", "x57",
	"x58", "x59", "x5a", "x5b", "x5c", "x5d", "x5e", "x5f",
	"x60", "x61", "x62", "x63", "x64", "x65", "x66", "x67",
	"x68", "x69", "x6a", "x6b", "x6c", "x6d", "x6e", "x6f",
	"x70", "x71", "x72", "x73", "x74", "x75", "x76", "x77",
	"x78", "x79", "x7a", "x7b", "x7c", "x7d", "x7e", "x7f",
	"x80", "x81", "x82", "x83", "x84", "x85", "x86", "x87",
	"x88", "x89", "x8a", "x8b", "x8c", "x8d", "x8e", "x8f",
	"x90", "x91", "x92", "x93", "x94", "x95", "x96", "x97",
	"x98", "x99", "x9a", "x9b", "x9c", "x9d", "x9e", "x9f",
	"xa0", "xa1", "xa2", "xa3", "xa4", "xa5", "xa6", "xa7",
	"xa8", "xa9", "xaa", "xab", "xac", "xad", "xae", "xaf",
	"xb0", "xb1", "xb2", "xb3", "xb4", "xb5", "xb6", "xb7",
	"xb8", "xb9", "xba", "xbb", "xbc", "xbd", "xbe", "xbf",
	"xc0", "xc1", "xc2", "xc3", "xc4", "xc5", "xc6", "xc7",
	"xc8", "xc9", "xca", "xcb", "xcc", "xcd", "xce", "xcf",
	"xd0", "xd1", "xd2", "xd3", "xd4", "xd5", "xd6", "xd7",
	"xd8", "xd9", "xda", "xdb", "xdc", "xdd", "xde", "xdf",
	"xe0", "xe1", "xe2", "xe3", "xe4", "xe5", "xe6", "xe7",
	"xe8", "xe9", "xea", "xeb", "xec", "xed", "xee", "xef",
	"xf0", "xf1", "xf2", "xf3", "xf4", "xf5", "xf6", "xf7",
	"xf8", "xf9", "xfa", "xfb", "xfc", "xfd", "xfe", "xff",
}

// bucket shards a token by its opening runes. The first three runes are decoded
// without materializing the whole token, but #492 preserves the legacy byte
// contract for invalid UTF-8 by returning to the []rune conversion whenever
// decoding observes an invalid byte in that prefix.
func bucket(tok string) string {
	var first, second rune
	end := 0
	count := 0
	for count < 3 && end < len(tok) {
		r, size := utf8.DecodeRuneInString(tok[end:])
		if r == utf8.RuneError && size == 1 {
			return bucketInvalidUTF8(tok)
		}
		switch count {
		case 0:
			first = r
		case 1:
			second = r
		}
		end += size
		count++
	}

	if count >= 2 && isShardASCII(first) && isShardASCII(second) {
		return safe(tok[:2])
	}

	h := fnv.New32a()
	_, _ = h.Write([]byte(tok[:end]))
	return hexBuckets[h.Sum32()%256]
}

// bucketInvalidUTF8 reproduces the legacy conversion before hashing. []rune
// replaces invalid bytes with U+FFFD, and converting the prefix back to a
// string re-encodes those replacements; skipping either step would move
// existing query terms to different buckets.
func bucketInvalidUTF8(tok string) string {
	runes := []rune(tok)
	if len(runes) >= 2 && isShardASCII(runes[0]) && isShardASCII(runes[1]) {
		return safe(string(runes[:2]))
	}
	if len(runes) > 3 {
		runes = runes[:3]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(string(runes)))
	return hexBuckets[h.Sum32()%256]
}

// cjkIndexKeys emits folded posting keys directly from CJK runs. It keeps the
// query-side bigram path independent and deduplicates folded pairs because the
// index callers already treat repeated posting keys as one key. The packed
// representation widens each rune before shifting so supplementary-plane
// values cannot overflow (#492).
func cjkIndexKeys(s string, emit func(tok string)) {
	// A CJK rune is never a single UTF-8 byte, so this guard preserves the
	// cjkBigrams fast path without decoding an ASCII-only message.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return
	}

	seen := map[uint64]struct{}{}
	emitFolded := func(r1, r2 rune) {
		// Grammar earns no posting. A pair of function runes — 的了, 在哪,
		// 什么 — is the CJK counterpart of a stop word: the query side has
		// dropped it from the term list since it was written, so the postings
		// were carrying the most frequent pairs in the language for nothing
		// (#492). queryKeys drops them from the AND for the same reason.
		if r1 != 0 && query.CJKFunctionRune(r1) && query.CJKFunctionRune(r2) {
			return
		}
		key := (uint64(r1) << 21) | uint64(r2)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		n := 1 + utf8.RuneLen(r2)
		if r1 != 0 {
			n += utf8.RuneLen(r1)
		}
		tok := make([]byte, n)
		tok[0] = 't'
		pos := 1
		if r1 != 0 {
			pos += utf8.EncodeRune(tok[pos:], r1)
		}
		utf8.EncodeRune(tok[pos:], r2)
		emit(string(tok))
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
