package search

import (
	"encoding/json"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The worn count is read from a log written with encoding/json, which replaces
// every byte that is not valid UTF-8 with U+FFFD. The id in the index keeps its
// bytes, so a session whose id holds such a byte was never recognised as one
// deja had already served (#2199).
func TestAWornCountReachesASessionWhoseIDIsNotValidUTF8(t *testing.T) {
	id := "deja-2026-08-27-app-" + string([]byte{0xff, 0xfe})
	// The key the log holds, produced the way the log produces it, rather than
	// asserted by hand.
	raw, err := json.Marshal([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	var back []string
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	logged := back[0]
	if logged == id {
		t.Fatalf("the id survived the round trip unchanged, so this measures nothing: %q", logged)
	}

	cold := rankSession("cold", "", "jwt refresh rotation fix applied")
	worn := rankSession(id, "", "jwt refresh rotation fix applied")
	hits, err := Run([]model.Session{cold, worn}, Options{
		Query: "jwt refresh rotation", All: true, RecallWorn: map[string]int{logged: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got Hit
	for _, h := range hits {
		if h.Session.ID == id {
			got = h
		}
	}
	if got.Session.ID != id {
		t.Fatalf("the session is not in the results at all: %+v", hits)
	}
	if got.Reused != 4 {
		t.Errorf("a session deja served four times is reported reused %d times", got.Reused)
	}
	if hits[0].Session.ID != id {
		t.Errorf("the worn session did not win the tie: %v", []string{hits[0].Session.ID, hits[1].Session.ID})
	}
}
