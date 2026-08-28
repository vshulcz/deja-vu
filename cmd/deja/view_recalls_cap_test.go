package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The Recalls tab carries the newest hundred injections and told the reader it
// carried every one of them — a claim the Notes tab beside it already knows how
// to qualify (#2313).
func TestThePageSaysTheRecallsTabIsAWindow(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for i := 0; i < 130; i++ {
		usage.RecordDigestInto(dir, usage.KindDejaVu,
			fmt.Sprintf("<deja-recall>\n  - digest-marker-%03d\n", i), fmt.Sprintf("agent%03d", i), 1, 100,
			[]string{"quaxbolt"}, "r0")
	}

	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the newest injection is on the page and an older one is not.
	if !strings.Contains(string(page), "digest-marker-129") {
		t.Fatalf("the newest injection is not on the page, so this measures nothing")
	}
	if strings.Contains(string(page), "digest-marker-000") {
		t.Fatalf("the page carries all 130 injections, so nothing is being held back")
	}
	if strings.Contains(string(page), "every injection an agent received") {
		t.Errorf("the page still claims to hold every injection")
	}
	if !strings.Contains(string(page), "of 130") {
		t.Errorf("the page does not say how many injections there are:\n%s", recallNote(string(page)))
	}
}

// recallNote is the sentence under the Recalls tab, for a readable failure.
func recallNote(page string) string {
	i := strings.Index(page, `id="rlist"`)
	if i < 0 {
		return "(no recalls tab)"
	}
	end := i + 400
	if end > len(page) {
		end = len(page)
	}
	return page[i:end]
}
