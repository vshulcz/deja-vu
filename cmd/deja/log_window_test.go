package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The audit trail stopped at twenty rows and said nothing, so twenty read as
// everything deja had served — the misread #2296 and #2299 are about, and the
// twenty is a default nobody typed (#2305).
func TestLogSaysWhenItStoppedAtTheDefault(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for i := 0; i < 25; i++ {
		usage.RecordDigest(dir, usage.KindDejaVu, "<deja-recall>\n  - Session: **a** `1`\n", 2, 1160)
	}

	var out bytes.Buffer
	if err := runLogTo(&out, dir, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if rows := strings.Count(got, usage.KindDejaVu); rows != 20 {
		t.Fatalf("printed %d rows, want the default 20", rows)
	}
	if !strings.Contains(got, "20 of 25") {
		t.Errorf("log stopped at twenty without saying so:\n%s", got)
	}

	// Asked for all of them, there is nothing to announce.
	out.Reset()
	if err := runLogTo(&out, dir, []string{"100"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), " of 25") {
		t.Errorf("a listing that held nothing back still announced a window:\n%s", out.String())
	}
}
