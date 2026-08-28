package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// Three injections to three agent sessions printed three identical rows: the
// recipient reached the digest log and never the event, so the audit list could
// not tell them apart and only the newest was readable, through --last (#2307).
func TestLogRowsNameTheAgentThatGotThem(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for _, into := range []string{"alpha", "beta", "gamma"} {
		usage.RecordDigestInto(dir, usage.KindDejaVu, "<deja-recall>\n  - Session: **a** `1`\n", into, 2, 232,
			[]string{"panic"}, "r0", "r1")
	}

	var out bytes.Buffer
	if err := runLogTo(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, into := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(got, into) {
			t.Errorf("the row for %q does not name it:\n%s", into, got)
		}
	}

	// An event recorded without a recipient — an MCP recall, say — prints no
	// empty label.
	hermeticEnv(t)
	dir = index.DefaultDir()
	usage.RecordServedSnapshot(dir, usage.KindRecall, "<deja-recall>\n  - Session: **b** `2`\n", 1, 100, []string{"b"}, "")
	out.Reset()
	if err := runLogTo(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "into") {
		t.Errorf("a row labels a recipient it does not have:\n%s", out.String())
	}
}
