package index

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A fork copies its source's turns with their times, so the two open alike;
// a session that asked the same thing at another moment does not (#4549).
func TestSessionOpeningMatchesACopyOnly(t *testing.T) {
	at := time.Date(2026, 10, 2, 7, 0, 0, 123e6, time.UTC)
	open := func(id string, t time.Time, text string) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{
			{Role: "assistant", Text: "hello", Time: t.Add(-time.Second)},
			{Role: "user", Text: text, Time: t},
		}}
	}
	src := SessionOpening(open("a", at, "the retry loop spins"))
	if src == 0 {
		t.Fatal("no opening for a session with a timed user turn")
	}
	if got := SessionOpening(open("f", at, "the retry loop spins")); got != src {
		t.Errorf("a copy opens differently: %x vs %x", got, src)
	}
	if got := SessionOpening(open("b", at.Add(time.Millisecond), "the retry loop spins")); got == src {
		t.Error("the same question a millisecond later reads as a copy")
	}
	if got := SessionOpening(open("c", at, "another question")); got == src {
		t.Error("another question at the same moment reads as a copy")
	}
	if got := SessionOpening(model.Session{Harness: "claude", Messages: []model.Message{{Role: "user", Text: "untimed"}}}); got != 0 {
		t.Errorf("an untimed turn has an opening: %x", got)
	}
}

// dsh delegates three deep and a grandchild names the child between them: the
// root's lineage takes in every descendant, a grandchild's every ancestor, and
// a cycle in bad data ends.
func TestSpawnedFamilyWalksEveryDepth(t *testing.T) {
	m := Manifest{Sessions: map[string]SessionMeta{
		"root":  {ID: "root"},
		"child": {ID: "child", Kind: "subagent", Parent: "root"},
		"grand": {ID: "grand", Kind: "subagent", Parent: "child"},
		"fork":  {ID: "fork", Kind: "fork", Parent: "root"},
		"loopa": {ID: "loopa", Kind: "subagent", Parent: "loopb"},
		"loopb": {ID: "loopb", Kind: "subagent", Parent: "loopa"},
		"self":  {ID: "self", Kind: "subagent", Parent: "self"},
	}}
	out := map[string]bool{}
	own := spawnedFamily(m, map[string]bool{"root": true}, out, nil)
	if !own["child"] || !own["grand"] || own["fork"] || own["root"] || len(out) != 0 {
		t.Errorf("root: own %v, ancestors %v", own, out)
	}
	out = map[string]bool{}
	own = spawnedFamily(m, map[string]bool{"grand": true}, out, nil)
	if !out["child"] || !out["root"] || len(own) != 0 {
		t.Errorf("grand: own %v, ancestors %v", own, out)
	}
	out = map[string]bool{}
	own = spawnedFamily(m, map[string]bool{"loopa": true, "self": true}, out, nil)
	if !out["loopb"] || !own["loopb"] || own["self"] {
		t.Errorf("cycle: own %v, ancestors %v", own, out)
	}
}
