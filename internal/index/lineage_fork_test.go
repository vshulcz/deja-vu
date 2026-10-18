package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// forkLineageStore indexes a cline source "a" that went on after its declared
// fork "f", a fork "g" of a that holds a's last turn, a sub-agent "x" of f, a
// sub-agent "y" of g, an undeclared copy "u" of a, a fork "h" of a copied with
// new times, and a dsh root with a child.
func forkLineageStore(t *testing.T) (string, func(role, text string, i int) model.Message) {
	t.Helper()
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	open := []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1)}
	with := func(extra ...model.Message) []model.Message {
		return append(append([]model.Message{}, open...), extra...)
	}
	ss := []model.Session{
		{Harness: "cline", ID: "a", Project: "p", Messages: with(turn("user", "source work after the fork: add jitter", 5))},
		{Harness: "cline", ID: "f", Project: "p", Kind: "fork", Parent: "a", Messages: with(turn("user", "fork work", 6))},
		{Harness: "cline", ID: "g", Project: "p", Kind: "fork", Parent: "a", Messages: with(turn("user", "source work after the fork: add jitter", 5), turn("user", "later fork work", 9))},
		{Harness: "cline", ID: "u", Project: "p", Messages: with(turn("user", "undeclared fork work", 8))},
		{Harness: "cline", ID: "x", Project: "p", Kind: "subagent", Parent: "f", Messages: []model.Message{turn("user", "read the code", 7)}},
		{Harness: "cline", ID: "y", Project: "p", Kind: "subagent", Parent: "g", Messages: []model.Message{turn("user", "read the tests", 10)}},
		// A Hermes /branch copies with new times, after the source's last turn.
		{Harness: "cline", ID: "h", Project: "p", Kind: "fork", Parent: "a", Messages: []model.Message{
			turn("user", "the retry loop spins", 12), turn("assistant", "backoff never resets", 13),
			turn("user", "source work after the fork: add jitter", 14), turn("user", "branch work", 15)}},
		{Harness: "dsh", ID: "root", Project: "p", Messages: []model.Message{turn("user", "plan the release", 20)}},
		{Harness: "dsh", ID: "child", Project: "p", Kind: "subagent", Parent: "root", Messages: []model.Message{turn("user", "draft the notes", 21)}},
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir, turn
}

// A declared fork leaves out its source only when it holds the source's last
// turn, the rule an undeclared copy already gets: a source that went on after
// the fork has work the fork never saw (#5293).
func TestLineageKeepsADeclaredForksSourceThatWentOn(t *testing.T) {
	dir, turn := forkLineageStore(t)
	cases := []struct {
		name string
		got  map[string]bool
		want []string
	}{
		{"declared fork f, source went on", Lineage(dir, map[string]bool{"f": true}), []string{"f", "x"}},
		{"declared fork g, holds the source's end", Lineage(dir, map[string]bool{"g": true}), []string{"a", "g", "y"}},
		{"fork h copied with new times after a's last turn", Lineage(dir, map[string]bool{"h": true}), []string{"a", "h"}},
		{"control, undeclared copy u", Lineage(dir, map[string]bool{"u": true}), []string{"u"}},
		// Read whole before any build: the same rule.
		{"whole fork of a, source went on", LineageWhole(dir, map[string]bool{"w": true}, model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
			Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "whole fork work", 11)}}), []string{"w"}},
		{"whole fork of a, holds its end", LineageWhole(dir, map[string]bool{"w": true}, model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
			Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "source work after the fork: add jitter", 5), turn("user", "whole fork work", 11)}}), []string{"a", "w"}},
	}
	for _, c := range cases {
		if got := lineageKeys(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The ancestor walk goes on through a fork and starts from a parent only an
// unindexed asker names: a sub-agent of a fork gets what the fork gets, and a
// new dsh grandchild leaves out the root the way its indexed parent does
// (#5305).
func TestLineageWalksThroughAForkAndAnUnindexedParent(t *testing.T) {
	dir, turn := forkLineageStore(t)
	grand := model.Session{Harness: "dsh", ID: "grand", Kind: "subagent", Parent: "child", Messages: []model.Message{turn("user", "check the links", 30)}}
	cases := []struct {
		name string
		got  map[string]bool
		want []string
	}{
		{"sub-agent y of g, which holds a's end", Lineage(dir, map[string]bool{"y": true}), []string{"a", "g", "y"}},
		{"sub-agent x of f, whose source went on", Lineage(dir, map[string]bool{"x": true}), []string{"f", "x"}},
		{"unindexed grandchild", Lineage(dir, map[string]bool{"grand": true}, grand), []string{"child", "grand", "root"}},
		{"control, indexed child", Lineage(dir, map[string]bool{"child": true}), []string{"child", "root"}},
	}
	for _, c := range cases {
		if got := lineageKeys(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A source left on the page for a fork is cut at the last turn they share,
// and one left out is not cut at all (#5335).
func TestLineageCutsASourceThatWentOnAtTheForkPoint(t *testing.T) {
	dir, turn := forkLineageStore(t)
	shared := turn("assistant", "", 1).Time
	for _, c := range []struct {
		name string
		ids  map[string]bool
	}{{"declared fork f", map[string]bool{"f": true}}, {"undeclared copy u", map[string]bool{"u": true}}} {
		out, cut := LineageCut(dir, c.ids)
		if out["a"] || !cut["a"].Equal(shared) {
			t.Errorf("%s: a hidden %v, cut %v, want kept and cut at %v", c.name, out["a"], cut["a"], shared)
		}
	}
	w := model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
		Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "whole fork work", 11)}}
	if _, cut := LineageWholeCut(dir, map[string]bool{"w": true}, w); !cut["a"].Equal(shared) {
		t.Errorf("whole fork: cut %v, want %v", cut["a"], shared)
	}
	if out, cut := LineageCut(dir, map[string]bool{"g": true}); !out["a"] || !cut["a"].IsZero() {
		t.Errorf("g holds a's end: a hidden %v, cut %v, want hidden and no cut", out["a"], cut["a"])
	}
}
