package index

import "testing"

// bucketPath refuses a name bucket did not make; if bucket ever made one the
// pattern misses, that token's postings would silently read as empty.
func TestEveryBucketNameIsAccepted(t *testing.T) {
	toks := []string{"a", "ab", "abc", "a1", "_x", "z9q", "Ab", "a-b", "a.b", "日本", "日", "привет", "ёж", "\xff\xfe", "a\xff", "ab\xff", "1", "x", "", "émoji😀", "😀"}
	for r := rune(0); r < 0x3000; r += 7 {
		toks = append(toks, string(r), string(r)+"a", "a"+string(r), string([]rune{r, r + 1, r + 2}))
	}
	for _, tok := range toks {
		if b := bucket(tok); !bucketName.MatchString(b) {
			t.Fatalf("bucket(%q) = %q, which bucketPath refuses", tok, b)
		}
	}
}
