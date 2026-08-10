package search

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Print renders search results to a terminal. The project name and session id
// are transcript/peer free text, and the lifecycle note is synced from another
// machine — an escape or bidi run in any of them would repaint or reorder the
// line. Snippets are already SafeText'd; these header fields must be too.
func TestSearchPrintSanitisesHeaderFields(t *testing.T) {
	var b bytes.Buffer
	hits := []Hit{{
		Session: model.Session{
			Harness: "claude", ID: "ev\u202eil\u200bid", Project: "proj\x1b[31m",
			Updated: time.Now(),
		},
		Count: 1, Tier: TierExact,
		Lifecycle: "rejected", LifecycleNote: "note\x1bfrom\u202e a peer\u200b",
		Moved:    "moved\x1bsince\u202e",
		Snippets: []string{"a clean snippet"},
	}}
	Print(&b, hits, Options{})
	out := b.String()
	for _, r := range out {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			t.Fatalf("search output carried a control rune %U: %q", r, out)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(out, bad) {
			t.Fatalf("search output carried %U: %q", bad, out)
		}
	}
	if !strings.Contains(out, "proj") {
		t.Fatalf("search dropped the project text: %q", out)
	}
}
