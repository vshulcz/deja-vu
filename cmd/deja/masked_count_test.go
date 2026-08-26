package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// The floor line is read before handing a file to someone else, so the number
// has to be the number of masked spots in the file. Counting the markers *and*
// adding what the pass replaced counted a fresh secret twice (#2061).
func TestTheMaskedFloorCountsWhatIsInTheFile(t *testing.T) {
	fresh := "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	alreadyMasked, _ := redact.Text("sk-ZYXWVUTSRQPONMLKJIHGFEDCBA9876543210zyxw")
	if !strings.Contains(alreadyMasked, redact.Marker) {
		t.Fatalf("the fixture's second secret was not masked: %q", alreadyMasked)
	}

	for _, c := range []struct {
		name, title, text string
		want              int
	}{
		{"one the export catches", "pool sizing", "the key is " + fresh, 1},
		{"one already masked at ingest", "pool sizing", "the key was " + alreadyMasked, 1},
		{"one of each", "pool sizing", "now " + fresh + " and before " + alreadyMasked, 2},
		{"nothing to mask", "pool sizing", "the pool size is the fix", 0},
	} {
		path := filepath.Join(t.TempDir(), "notes.md")
		masked, err := exportPromoted(path, c.title, c.text, "claude:s9", "accepted", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The premise and the answer are the same thing: what the line claims
		// has to be what the file holds.
		inFile := strings.Count(string(b), redact.Marker)
		if inFile != c.want {
			t.Fatalf("%s: the file holds %d masked spots, so the case is wrong: %s", c.name, inFile, b)
		}
		if masked != c.want {
			t.Errorf("%s: the line says %d, the file holds %d", c.name, masked, inFile)
		}
	}
}

// The other surface that prints the floor, counted the same way.
func TestShareCountsWhatIsInTheShare(t *testing.T) {
	fresh := "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd"
	var out strings.Builder
	stderr := captureStderr(t, func() { printSanitized(&out, "the key is "+fresh+"\nand nothing else") })
	inShare := strings.Count(out.String(), redact.Marker)
	if inShare != 1 {
		t.Fatalf("the share holds %d masked spots, so this measures nothing: %q", inShare, out.String())
	}
	if !strings.Contains(stderr, "deja: 1 secret masked") {
		t.Errorf("the floor line disagrees with the %d masked spots in the share: %q", inShare, strings.TrimSpace(stderr))
	}
}
