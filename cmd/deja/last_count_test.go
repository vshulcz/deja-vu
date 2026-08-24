package main

import (
	"strings"
	"testing"
)

// `deja last` took its count from the first bare argument that parsed as a
// number and dropped everything else without a word, so `deja last api-gateway`
// — the filter spelled `--project api-gateway` in the help — returned the
// default ten sessions from every project (#1618).
func TestLastRefusesAnArgumentThatIsNotACount(t *testing.T) {
	for _, arg := range []string{"abc", "api-gateway", "0", "3.5"} {
		n, _, _, err := parseLast([]string{arg})
		if err == nil {
			t.Errorf("last %q was accepted and left the count at %d", arg, n)
			continue
		}
		if !strings.Contains(err.Error(), arg) {
			t.Errorf("last %q: the refusal does not name what was typed: %v", arg, err)
		}
	}
	// The controls: a real count still works, with or without flags, and an
	// omitted count keeps the default.
	if n, _, _, err := parseLast([]string{"3"}); err != nil || n != 3 {
		t.Errorf("last 3 = %d, %v", n, err)
	}
	if n, o, _, err := parseLast([]string{"3", "--project", "api"}); err != nil || n != 3 || o.Project != "api" {
		t.Errorf("last 3 --project api = %d, %q, %v", n, o.Project, err)
	}
	if n, _, _, err := parseLast(nil); err != nil || n != 10 {
		t.Errorf("last = %d, %v; want the default 10", n, err)
	}
}
