package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedGaveUpSessions writes two sessions that both backed an approach out: one a
// reader can scan, one the size of this machine's marathons.
func seedGaveUpSessions(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id string, filler int) {
		var b strings.Builder
		turn := func(role, text, stamp string) {
			b.WriteString(`{"type":"` + role + `","sessionId":"` + id + `","timestamp":"` + stamp +
				`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n")
		}
		turn("user", "the flimsyshard exporter retries too hard", "2026-03-01T10:00:00Z")
		turn("assistant", "we backed out the jitter change and capped flimsyshard retries at three instead",
			"2026-03-01T10:01:00Z")
		for i := range filler {
			turn("assistant", strings.Repeat("flimsyshard retry notes and more notes ", 60),
				"2026-03-01T1"+string(rune('0'+i%10))+":0"+string(rune('0'+i%6))+":00Z")
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Six words per filler phrase, sixty phrases per turn: a hundred turns is
	// past the thirty thousand words where "somewhere in here" stops being a
	// place.
	write("short1", 2)
	write("marathon1", 100)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The mark is a property of the whole session, so on a long one it says only
// that something was dropped somewhere in it. On sixteen recall calls against a
// real store the answer carried it 32 times — twice per page — and every one of
// those sessions was far too long to scan. The search screen got this rule in
// #3474; the answer an agent reads was still printing the mark on everything.
func TestTheAnswerDoesNotMarkAMarathonAsHavingBackedOut(t *testing.T) {
	dir := seedGaveUpSessions(t)
	text, _, _, _, err := recallTextResult(dir, "flimsyshard exporter retries", "", 5, 0, 8192)
	if err != nil {
		t.Fatal(err)
	}
	const mark = "abandoned one approach partway"
	short := strings.Index(text, "short1")
	long := strings.Index(text, "marathon1")
	if short < 0 || long < 0 {
		t.Fatalf("the page is missing one of the two sessions:\n%s", text)
	}
	marks := strings.Count(text, mark)
	if marks != 1 {
		t.Errorf("the mark appears %d times; only the session a reader can scan should carry it:\n%s", marks, text)
	}
	// And it is the short session's: the mark sits under its header, before the
	// next one begins.
	between := text[min(short, long):max(short, long)]
	if short < long && !strings.Contains(between, mark) {
		t.Errorf("the mark went to the marathon rather than the short session:\n%s", text)
	}
	if long < short && strings.Contains(between, mark) {
		t.Errorf("the mark went to the marathon rather than the short session:\n%s", text)
	}
}
