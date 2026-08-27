package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// `.hookseen` is a line of fields and the agent session id is written into it
// verbatim, though it arrives in the hook payload — whatever the host sends. A
// space cost that session its dedup and gave its entries to another id; a
// newline let a payload write a line under any id it liked (#2167).
func TestADedupEntryStaysUnderTheSessionThatOwnsIt(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	for _, sid := range []string{"plain-1", "two words", "forge\nvictim", "tab\tsep"} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		rememberInjectedIDs(dir, sid, "tok")
		rememberInjected(dir, sid, []model.Session{{ID: "sess-a"}})

		// Its own dedup works, whatever the host called it.
		seen := alreadyInjected(dir, sid)
		if !seen["tok"] || !seen["sess-a"] {
			t.Errorf("session %q does not remember what it was shown: %v", sid, seen)
		}
		// The file stays one line per entry: a newline in the id cannot open a
		// line of its own.
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(strings.Split(strings.TrimRight(string(body), "\n"), "\n")); n != 2 {
			t.Errorf("session %q wrote %d lines for two entries:\n%q", sid, n, body)
		}
		// And nothing lands under another id — neither the first word of this
		// one nor the tail after a newline.
		for _, other := range strings.FieldsFunc(sid, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\t'
		}) {
			if other == sid {
				continue
			}
			if got := alreadyInjected(dir, other); len(got) > 0 {
				t.Errorf("a session called %q sees %v, written by a payload calling itself %q", other, got, sid)
			}
		}
		// Forgetting is by the same key, so a session can still drop its own.
		forgetInjected(dir, sid)
		if got := alreadyInjected(dir, sid); len(got) > 0 {
			t.Errorf("session %q could not forget its entries: %v", sid, got)
		}
	}
}

// The value side is not mapped — it is looked up against the index's own ids,
// where a mapped one would match nothing. A value that cannot be a field is
// dropped instead, which costs a second showing rather than the entry written
// after it (#2167).
func TestAValueThatCannotBeAFieldIsDroppedNotMangled(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rememberInjectedIDs(dir, "ses1", "tok one", "tok-two")
	rememberInjected(dir, "ses1", []model.Session{{ID: "sess a"}, {ID: "sess-b"}})
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if n := len(strings.Fields(line)); n != 2 && n != 3 {
			t.Errorf("a line of %d fields: %q", n, line)
		}
	}
	seen := alreadyInjected(dir, "ses1")
	for _, want := range []string{"tok-two", "sess-b"} {
		if !seen[want] {
			t.Errorf("%q was not recorded", want)
		}
	}
	// The ids that could not be written are simply absent — the index looks
	// them up by their own spelling, so a mapped stand-in would match nothing.
	for _, gone := range []string{"tok", "one", "tok_one", "sess", "a", "sess_a"} {
		if seen[gone] {
			t.Errorf("%q reads as already shown, though it was never written", gone)
		}
	}
}
