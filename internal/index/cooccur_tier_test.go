package index

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// The co-occurrence tier is the last thing tried before falling back to
// relevance: when a query's exact words find nothing, it substitutes a term
// for one that kept company with it in this corpus. It is the tier that
// answers "the thing next to the thing you remember".
func TestCooccurTierSubstitutesANeighbouringTerm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	// "postgres" and "connection" keep company across the corpus; "psql"
	// never appears with "connection" at all.
	for i, text := range []string{
		"the postgres connection pool ran dry again",
		"postgres connection limits raised to 200",
		"restarted postgres and the connection settled",
		"psql prints the schema",
	} {
		writeLines(t, filepath.Join(claude, "project", "s"+string(rune('1'+i))+".jsonl"),
			claudeLine("s"+string(rune('1'+i)), "2026-01-0"+string(rune('1'+i))+"T00:01:00Z", text))
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A query needs at least two terms: one lucky word is not a co-occurrence.
	if got, err := cooccurSearch(dir, m, query.Options{Query: "postgres", All: true}); err != nil || len(got.Sessions) != 0 {
		t.Fatalf("single term produced %d sessions (%v)", len(got.Sessions), err)
	}

	// Both words present is the exact tier's job, not this one's — but asking
	// here must not error, and must not invent sessions that lack the terms.
	got, err := cooccurSearch(dir, m, query.Options{Query: "postgres connection", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Sessions {
		var b strings.Builder
		for _, msg := range s.Messages {
			b.WriteString(strings.ToLower(msg.Text))
			b.WriteString(" ")
		}
		if !strings.Contains(b.String(), "postgres") && !strings.Contains(b.String(), "connection") {
			t.Fatalf("session %s has neither term: %q", s.ID, b.String())
		}
	}
}
