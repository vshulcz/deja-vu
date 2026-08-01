package index

import "testing"

// The indexing line is the first output anyone sees from deja (#737).
func TestPluralS(t *testing.T) {
	for n, want := range map[int]string{0: "s", 1: "", 2: "s", 42: "s"} {
		if got := pluralS(n); got != want {
			t.Errorf("pluralS(%d) = %q, want %q", n, got, want)
		}
	}
}
