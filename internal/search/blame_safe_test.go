package search

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/model"
)

// PrintBlame renders to a terminal and, through the MCP blame tool, to an agent.
// id, project and title are transcript free text — an imported peer's title
// most of all — so an escape or a bidi run in them must not reach the output.
func TestPrintBlameSanitisesSessionFields(t *testing.T) {
	var b bytes.Buffer
	hits := []BlameHit{{
		Session: model.Session{
			Harness: "claude", ID: "ev\u202eil\u200bid", Project: "proj\x1b[31m",
			Updated: time.Now(),
		},
		Title: "hostile\x1btitle\u202e here", Count: 1, Tier: "exact",
		Snippets:      []string{"x"},
		Lifecycle:     "rejected",
		LifecycleNote: "note\x1bfrom\u202e a peer\u200b",
	}}
	PrintBlame(&b, hits, false)
	out := b.String()
	for _, r := range out {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			t.Fatalf("blame output carried a control rune %U: %q", r, out)
		}
	}
	for _, bad := range []rune{'\u202e', '\u200b'} {
		if strings.ContainsRune(out, bad) {
			t.Fatalf("blame output carried %U: %q", bad, out)
		}
	}
	if !strings.Contains(out, "hostile") || !strings.Contains(out, "proj") {
		t.Fatalf("blame dropped the readable text: %q", out)
	}
}
