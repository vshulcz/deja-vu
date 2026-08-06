package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja share` ends with "review before sending". A U+202E reverses the
// rendering of everything after it, so the reviewer reads one thing and sends
// another (#1081).
func TestShareOutputCarriesNoInvisibleReordering(t *testing.T) {
	const payload = "AUDITK terminal test RED\u202ereversed\u200bzerowidth"
	var out bytes.Buffer
	printSanitized(&out, payload)
	got := out.String()

	for _, r := range got {
		if unicode.Is(unicode.Cf, r) {
			t.Errorf("share output carries %U, which renders as something other than it reads: %q", r, got)
		}
	}
	// The words themselves must survive — this is a share, not a redaction.
	for _, want := range []string{"AUDITK terminal test", "RED", "reversed", "zerowidth"} {
		if !strings.Contains(got, want) {
			t.Errorf("share dropped %q from %q", want, got)
		}
	}
}

// End to end: the escape sequences are dropped upstream in the digest, the
// invisible reordering here. A share that has been through the whole command
// must carry neither.
func TestShareCommandDropsEscapesAndInvisibles(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-secretproj", "sh1.jsonl"), "sh1", []string{
		`{"type":"user","sessionId":"sh1","timestamp":"2026-05-01T10:00:00Z","message":{"role":"user","content":"AUDITK terminal test \u001b[2J\u001b[31mRED\u202ereversed\u200bzerowidth"}}`,
		`{"type":"assistant","sessionId":"sh1","timestamp":"2026-05-01T10:01:00Z","message":{"role":"assistant","content":"AUDITK we set the staging config"}}`,
	})
	if err := index.Ensure(os.Getenv("DEJA_INDEX_DIR"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := captureRun(t, "share", "sh1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Errorf("shared document carries %U: %q", r, got)
		}
	}
	if !strings.Contains(got, "AUDITK terminal test") {
		t.Errorf("share lost the turn entirely:\n%s", got)
	}
}

// Joiners are load-bearing in several scripts and in emoji sequences; a share
// that mangles them to defend against an invisible character has made the
// wrong trade.
func TestShareKeepsJoiners(t *testing.T) {
	const text = "AUDITK family \U0001F468\u200d\U0001F469\u200d\U0001F467 and zero\u200cwidth-non-joiner"
	var out bytes.Buffer
	printSanitized(&out, text)
	if got := out.String(); !strings.Contains(got, "\u200d") || !strings.Contains(got, "\u200c") {
		t.Errorf("share stripped a joiner: %q", got)
	}
}
