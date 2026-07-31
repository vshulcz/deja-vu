package main

import (
	"bytes"
	"strings"
	"testing"
)

// A misspelled subcommand is searched for as a word — `deja isntall` corrects
// to "install" and returns sessions that mention installing, which is not what
// the typist wanted. Spelled correctly it would have run the command, so the
// hint only ever fires on the case where it is wanted.
func TestFuzzyNamesTheCommandBehindATypo(t *testing.T) {
	var b bytes.Buffer
	printFuzzy(&b, map[string][]string{"isntall": {"install"}})
	got := b.String()
	if !strings.Contains(got, "isntall -> install") {
		t.Fatalf("the correction itself must still be reported: %q", got)
	}
	if !strings.Contains(got, "deja install") {
		t.Fatalf("a corrected subcommand should be offered: %q", got)
	}

	// An ordinary word that happens to be misspelled gets no command hint.
	b.Reset()
	printFuzzy(&b, map[string][]string{"pgbouncr": {"pgbouncer"}})
	if strings.Contains(b.String(), "is also a command") {
		t.Fatalf("no command is named pgbouncer: %q", b.String())
	}

	// One hint is enough however many terms were corrected.
	b.Reset()
	printFuzzy(&b, map[string][]string{"isntall": {"install"}, "resotre": {"restore"}})
	if n := strings.Count(b.String(), "is also a command"); n != 1 {
		t.Fatalf("hinted %d times, want once: %q", n, b.String())
	}
}
