package peers

import (
	"testing"
	"time"
)

// ssh lowercases a host before it matches it, and a DNS name is
// case-insensitive (RFC 4343), so `Laptop` and `laptop` are one machine.
// Comparing byte for byte made them two: deja opened two connections to it,
// gave each row its own watermark — so the second pushed everything the first
// had delivered — and reported the machine twice, with the stale row's failure
// beside the fresh row's success. That is #1853's harm, reached this time by
// deja's own writes rather than a hand-edit (#1867).
func TestOneMachineNamedInTwoCasesIsOneRow(t *testing.T) {
	writePeers(t, `{"peers":[{"host":"Laptop","last_push":"2026-08-20T10:00:00Z"}]}`)

	when := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	if err := Record("laptop", false, when, nil); err != nil {
		t.Fatal(err)
	}
	list := Load()
	if len(list) != 1 {
		t.Fatalf("a second spelling of one machine made a second row: %#v", list)
	}
	// The spelling stays as it was written: it is what the report prints and
	// what `sessions_from_there` is counted against.
	if list[0].Host != "Laptop" {
		t.Errorf("the stored spelling changed to %q", list[0].Host)
	}
	if !list[0].LastPush.Equal(when) {
		t.Errorf("the exchange landed on neither row: %#v", list[0])
	}
}

// A file can hold both spellings already — deja wrote them before this rule.
func TestBothSpellingsInTheFileFoldToOneRow(t *testing.T) {
	writePeers(t, `{"peers":[
		{"host":"Laptop","last_push":"2026-08-20T10:00:00Z"},
		{"host":"laptop","last_push":"2026-08-25T09:00:00Z"}
	]}`)
	list := Load()
	if len(list) != 1 {
		t.Fatalf("want one machine, got %#v", list)
	}
	if got := list[0].LastPush.UTC().Format(time.RFC3339); got != "2026-08-25T09:00:00Z" {
		t.Errorf("the folded row kept the older stamp: %s", got)
	}
}

// `deja sync forget` is how a typo'd host stops being retried by every bare
// sync, and it matched only the spelling that was written.
func TestForgetMatchesAnySpellingOfTheHost(t *testing.T) {
	writePeers(t, `{"peers":[{"host":"Laptop","last_push":"2026-08-20T10:00:00Z"}]}`)
	found, err := Forget("LAPTOP")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("forget did not find the machine under another spelling")
	}
	if list := Load(); len(list) != 0 {
		t.Errorf("the row survived: %#v", list)
	}
}

// The watermark is keyed by the peer string, so a run has to use the spelling
// the machine is stored under or it pushes everything again.
func TestCanonicalGivesTheStoredSpelling(t *testing.T) {
	writePeers(t, `{"peers":[{"host":"Laptop","last_push":"2026-08-20T10:00:00Z"}]}`)
	if got := Canonical("LAPTOP"); got != "Laptop" {
		t.Errorf("Canonical = %q, want the stored spelling", got)
	}
	// A machine deja has never heard of keeps the name as typed — there is no
	// other spelling to prefer, and the name is what ssh is handed.
	if got := Canonical(" mini "); got != "mini" {
		t.Errorf("Canonical of an unknown host = %q, want it as typed", got)
	}
}

// The user half of user@host is an account name, not a hostname: Root@box and
// root@box are two logins, and folding the whole string would merge them.
func TestTheUserHalfOfAnAddressKeepsItsCase(t *testing.T) {
	writePeers(t, `{"peers":[{"host":"Root@box","last_push":"2026-08-20T10:00:00Z"}]}`)
	if err := Record("root@box", false, time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 2 {
		t.Fatalf("two accounts on one host were merged into one row: %#v", list)
	}
	if err := Record("root@BOX", false, time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatal(err)
	}
	if list := Load(); len(list) != 2 {
		t.Fatalf("the host half was not folded: %#v", list)
	}
}
