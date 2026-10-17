package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func lineageKeys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// lineageStore indexes an asker "a", a copy "c" of a's opening that a went on
// past, a fork "d" that went on past what it copied, a session "u" in the same
// project that opened on its own, and a sub-agent "s" spawned by "a".
func lineageStore(t *testing.T) string {
	t.Helper()
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	opening := []model.Message{
		turn("user", "the retry loop spins forever", 0),
		turn("assistant", "the backoff never resets", 1),
	}
	with := func(extra ...model.Message) []model.Message {
		return append(append([]model.Message{}, opening...), extra...)
	}
	ss := []model.Session{
		{Harness: "claude", ID: "a", Project: "p", Messages: with(turn("user", "reset it on success", 2))},
		{Harness: "claude", ID: "c", Project: "p", Messages: with()},
		{Harness: "claude", ID: "d", Project: "p", Messages: with(turn("user", "try a jittered cap instead", 3))},
		{Harness: "claude", ID: "u", Project: "p", Messages: []model.Message{turn("user", "the retry loop spins forever", 9)}},
		{Harness: "claude", ID: "s", Project: "p", Kind: "subagent", Parent: "a", Messages: []model.Message{turn("user", "read the backoff code", 5)}},
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The asker's lineage is itself, its sub-agent, and the copy it already holds
// the end of. A fork that went on past the copy and a session that only shares
// the project stay answerable (#4547, #4549).
func TestLineageTakesTheHeldCopyAndSubAgentOnly(t *testing.T) {
	dir := lineageStore(t)
	got := lineageKeys(Lineage(dir, map[string]bool{"a": true}))
	if want := []string{"a", "c", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a = %v, want %v", got, want)
	}
	// The sub-agent asking counts its parent as itself (#4548).
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"s": true})), []string{"a", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of s = %v, want %v", got, want)
	}
	// The diverged fork holds the copy's end too, but not its sibling a's,
	// which went on past the copy and so still has news for it.
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"d": true})), []string{"c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of d = %v, want %v", got, want)
	}
}

// An asker the index has not seen yet is the newer copy of everything it opens
// alike with, and a fork it names as its source is its own work.
func TestLineageHeadMatchesByOpeningAndNamedParent(t *testing.T) {
	dir := lineageStore(t)
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	head := model.Session{Harness: "claude", ID: "h", Kind: "fork", Parent: "u", Messages: []model.Message{
		{Role: "user", Text: "the retry loop spins forever", Time: at},
	}}
	got := lineageKeys(Lineage(dir, map[string]bool{"h": true}, head))
	if want := []string{"a", "c", "d", "h", "u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of an unindexed fork = %v, want %v", got, want)
	}
	// A head that is not the asker says nothing about the asker.
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"u": true}, head)), []string{"u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a head not asking widened the lineage: %v, want %v", got, want)
	}
}

// A missing or unreadable manifest leaves the asker and nothing it cannot
// prove, so recall never hides a session it could not read.
func TestLineageWithoutAManifestIsTheAskerOnly(t *testing.T) {
	if got := Lineage(t.TempDir(), map[string]bool{"": true}); got != nil {
		t.Errorf("an empty id produced a lineage: %v", got)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	head := model.Session{Harness: "claude", ID: "h", Kind: "subagent", Parent: "p"}
	if got, want := lineageKeys(Lineage(missing, map[string]bool{"h": true}, head)), []string{"h", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing store: %v, want %v", got, want)
	}
	corrupt := t.TempDir()
	if err := os.WriteFile(filepath.Join(corrupt, "manifest.gob"), []byte("not a gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := lineageKeys(Lineage(corrupt, map[string]bool{"a": true})), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("corrupt manifest: %v, want %v", got, want)
	}
	if HasSession(missing, "a") || HasSession(corrupt, "a") {
		t.Error("HasSession answered yes without a readable manifest")
	}
}

// When the record log cannot be read, whether the asker holds a copy cannot be
// told, so the copy stays on the page; the sub-agent edge, read off the
// manifest alone, still holds.
func TestLineageKeepsTheCopyWhenRecordsAreUnreadable(t *testing.T) {
	dir := lineageStore(t)
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if got, want := lineageKeys(Lineage(dir, map[string]bool{"a": true})), []string{"a", "s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage without records = %v, want %v", got, want)
	}
}

func TestHasSession(t *testing.T) {
	dir := lineageStore(t)
	if !HasSession(dir, "a") {
		t.Error("an indexed session is not found")
	}
	if HasSession(dir, "zzz") {
		t.Error("an unknown id is found")
	}
	if HasSession(dir, "") {
		t.Error("the empty id is found")
	}
}

// An asker read off its store in full leaves out only the indexed sessions
// whose last turn it holds: the copy it was forked from, not the sibling or
// the source that went on past it (#5129).
func TestLineageWholeLeavesOutOnlyWhatTheAskerHolds(t *testing.T) {
	dir := lineageStore(t)
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	opening := []model.Message{
		turn("user", "the retry loop spins forever", 0),
		turn("assistant", "the backoff never resets", 1),
	}
	// A fork of c: holds c's end, not a's or d's.
	w := model.Session{Harness: "claude", ID: "w", Kind: "fork", Messages: append(append([]model.Message{}, opening...),
		model.Message{Role: "user", Text: "untimed, not a turn"},
		turn("user", "what about a circuit breaker", 7))}
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"w": true}, w)), []string{"c", "w"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a whole fork of c = %v, want %v", got, want)
	}
	// A fork of a holds a's end, and with it c's.
	wa := model.Session{Harness: "claude", ID: "wa", Messages: append(append([]model.Message{}, opening...),
		turn("user", "reset it on success", 2), turn("user", "now add a test", 8))}
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"wa": true}, wa)), []string{"a", "c", "wa"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of a whole fork of a = %v, want %v", got, want)
	}
	// A whole session that is not the asker says nothing about the asker.
	if got, want := lineageKeys(LineageWhole(dir, map[string]bool{"u": true}, wa)), []string{"u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lineage of u with an unrelated whole = %v, want %v", got, want)
	}
}
