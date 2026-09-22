package index

import (
	"path/filepath"
	"testing"
)

// BuiltSummary reads the manifest the ingest wrote: sessions, the harnesses
// they came from, and the asked hashes that appear in more than one session.
func TestBuiltSummaryCountsFromTheManifest(t *testing.T) {
	q := "why does the retry loop drop the last attempt"
	dir := askedFixture(t, map[string][]string{
		"a": {q},
		"b": {q},
		"c": {"how do I run the integration tests here"},
	}, map[string]string{
		"a": "2026-03-01T10:00:00Z",
		"b": "2026-05-01T10:00:00Z",
		"c": "2026-06-01T10:00:00Z",
	})
	sessions, harnesses, repeated := BuiltSummary(dir, nil)
	if sessions != 3 || harnesses != 1 || repeated != 1 {
		t.Fatalf("summary = %d sessions, %d harnesses, %d repeated; want 3, 1, 1", sessions, harnesses, repeated)
	}
	// The trust gate: a rule that withholds the project withholds the count.
	if s, h, r := BuiltSummary(dir, func(string) bool { return false }); s+h+r != 0 {
		t.Fatalf("a gate that allows nothing still counted %d/%d/%d", s, h, r)
	}
	if s, _, r := BuiltSummary(dir, func(string) bool { return true }); s != 3 || r != 1 {
		t.Fatalf("a gate that allows everything counted %d sessions, %d repeated", s, r)
	}
}

func TestBuiltSummaryIsZeroWithoutAManifest(t *testing.T) {
	tmp := t.TempDir()
	if s, h, r := BuiltSummary(filepath.Join(tmp, "missing"), nil); s+h+r != 0 {
		t.Fatalf("no manifest, yet %d/%d/%d", s, h, r)
	}
	// "" means the default directory, which under a hermetic home is empty too.
	setHome(t, tmp)
	t.Setenv("DEJA_INDEX_DIR", "")
	if s, h, r := BuiltSummary("", nil); s+h+r != 0 {
		t.Fatalf("default dir under an empty home, yet %d/%d/%d", s, h, r)
	}
}

// A resumed conversation is written again under a new key and carries every
// question the original did. The built note's count is the brief's rule: one
// conversation written twice is not a question asked twice.
func TestBuiltSummarySkipsAResumedConversation(t *testing.T) {
	q := "why does the retry loop drop the last attempt"
	rest := []string{"where does the scheduler read its failover timeout", "how do I run the integration tests here"}
	dir := askedFixture(t, map[string][]string{
		"orig":   append([]string{q}, rest...),
		"resume": append([]string{q}, rest...),
		"other":  {"what is the deploy command for staging here"},
	}, map[string]string{
		"orig":   "2026-03-01T10:00:00Z",
		"resume": "2026-03-04T10:00:00Z",
		"other":  "2026-06-01T10:00:00Z",
	})
	if _, _, repeated := BuiltSummary(dir, nil); repeated != 0 {
		t.Fatalf("a resumed conversation counted as %d repeated question(s)", repeated)
	}
}
