package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedRecallPage writes enough sessions that one recall page fills its budget.
func seedRecallPage(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for s := range 12 {
		var b strings.Builder
		for i := range 20 {
			text := strings.Repeat("grumblewidget retry budget ", 30)
			b.WriteString(`{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"2026-08-0` +
				string(rune('1'+s%9)) + `T1` + string(rune('0'+i%10)) + `:00:00Z","sessionId":"sess` +
				string(rune('a'+s)) + `","cwd":"/proj"}` + "\n")
		}
		name := "sess" + string(rune('a'+s)) + ".jsonl"
		if err := os.WriteFile(filepath.Join(store, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every truncated excerpt in a recall payload ends with … except the one the
// page budget cut, which ended mid-word saying nothing (#1799).
func TestTheLastExcerptOnAFullPageSaysItWasCut(t *testing.T) {
	dir := seedRecallPage(t)
	text, _, _, _, err := recallTextResult(dir, "grumblewidget retry budget", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if len(frameRecall(text)) > 4096 {
		t.Fatalf("the page is %d bytes, over its 4096 budget", len(frameRecall(text)))
	}
	body := text
	if i := strings.Index(body, "more match(es)"); i > 0 {
		body = strings.TrimSpace(body[:strings.LastIndex(body[:i], "\n")])
	}
	last := body[strings.LastIndexByte(body, '\n')+1:]
	if !strings.HasSuffix(strings.TrimSpace(last), "…") {
		t.Errorf("the cut line does not say it was cut:\n%q", last)
	}
}

// The control: a page well inside its budget is not marked as cut.
func TestAShortPageIsNotMarkedAsCut(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the pool exhausted again"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"short","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "short.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	text, _, _, _, err := recallTextResult(dir, "pool exhausted", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "…\n") && !strings.Contains(text, "pool exhausted") {
		t.Errorf("a page inside its budget was marked as cut:\n%s", text)
	}
}

// The marker is reserved out of the budget, never added on top of it: a budget
// too small to hold both the page and the marker keeps the bytes and drops the
// marker.
func TestTheCutMarkerNeverBreaksTheBudget(t *testing.T) {
	dir := seedRecallPage(t)
	for _, budget := range []int{1, 5, 10, 60, 200, 1000, 4096 - recallFrameOverhead} {
		text, _, _, _, err := recallTextResult(dir, "grumblewidget retry budget", "", 0, 0, budget)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if len(text) > budget {
			t.Errorf("budget %d produced %d bytes", budget, len(text))
		}
		if strings.Contains(text, "… …") {
			t.Errorf("budget %d marked the same cut twice:\n%s", budget, text)
		}
	}
}

// A trim landing between an excerpt's own ellipsis and its newline must not
// mark the same cut twice.
func TestACutOnAnEllipsisIsNotMarkedTwice(t *testing.T) {
	for _, in := range []string{"- a shortened excerpt …", "- a shortened excerpt … ", "- an ordinary excerpt"} {
		out := markCut(in)
		if strings.Contains(out, "… …") {
			t.Errorf("%q was marked twice: %q", in, out)
		}
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("%q did not end the line: %q", in, out)
		}
	}
	if got := markCut("- an ordinary excerpt"); got != "- an ordinary excerpt"+cutMarker {
		t.Errorf("an unmarked cut lost its marker: %q", got)
	}
}
