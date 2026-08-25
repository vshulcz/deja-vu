package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedConclusionCorpus writes sessions whose decision lines are worded nothing
// like the query, so the conclusions block under the best hit is the only place
// the outcome appears.
func seedConclusionCorpus(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for s := range 6 {
		var b strings.Builder
		for i := range 30 {
			text := strings.Repeat("flimsyshard retry cap discussion ", 8)
			if i%7 == 6 {
				text = "so we decided to cap flimsyshard retries at three and log the fourth, " +
					strings.Repeat("because the queue drains slower than it fills ", 6)
			}
			b.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"` + text +
				`"},"timestamp":"2026-08-0` + string(rune('1'+s)) + `T1` + string(rune('0'+i%10)) +
				`:00:00Z","sessionId":"cs` + string(rune('a'+s)) + `","cwd":"/proj"}` + "\n")
		}
		if err := os.WriteFile(filepath.Join(store, "cs"+string(rune('a'+s))+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The conclusions gate under the best hit reads `budget - headerRoom + hb.Len()
// - reserve`, and the comment on it (#1319) says the plus is deliberate and
// measured: at 4096, 3000 and 2400 the payload is the same either way, and the
// outer trim is what bounds the page. That was left as prose. These are the
// numbers, so a future change to the sign has something to fail against.
func TestTheConclusionsBlockSurvivesEveryBudgetItWasMeasuredAt(t *testing.T) {
	dir := seedConclusionCorpus(t)
	for _, budget := range []int{4096 - recallFrameOverhead, 3000, 2400} {
		text, sessions, _, _, err := recallTextResult(dir, "flimsyshard retry cap", "", 0, 0, budget)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if len(text) != budget {
			t.Errorf("budget %d produced %d bytes; the page is meant to fill it", budget, len(text))
		}
		if sessions < 3 {
			t.Errorf("budget %d served %d sessions; the page stopped filling", budget, sessions)
		}
		if !strings.Contains(text, "decided to cap") {
			t.Errorf("budget %d dropped the conclusions block:\n%s", budget, text)
		}
	}
}

// Below those budgets the block is withheld, and that is the branch the comment
// says is the only thing the sign changes. Pinned so the boundary is a fact
// rather than folklore: at 1200 the page still carries the outcome, at 600 it
// is excerpts only.
func TestTheConclusionsBlockIsWithheldOnASmallPage(t *testing.T) {
	dir := seedConclusionCorpus(t)
	withBlock, _, _, _, err := recallTextResult(dir, "flimsyshard retry cap", "", 0, 0, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withBlock, "decided to cap") {
		t.Errorf("a 1200-byte page no longer carries the outcome:\n%s", withBlock)
	}
	small, _, _, _, err := recallTextResult(dir, "flimsyshard retry cap", "", 0, 0, 600)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(small, "decided to cap") {
		t.Errorf("a 600-byte page spent its budget on the block:\n%s", small)
	}
	if len(small) != 600 {
		t.Errorf("the small page is %d bytes, not the 600 it was given", len(small))
	}
}
