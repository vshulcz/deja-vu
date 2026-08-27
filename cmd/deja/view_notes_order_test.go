package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// #2112 sorts the notes newest-first in Go before it cuts them, and nothing
// pinned the order that reaches the page — the cap test only asks which notes
// are on it. This reads the array the page hands its script, and then asks the
// script itself whether it draws that array as given: the order the reader
// sees is the two together, and either half can move on its own.
func TestTheNotesTabIsRenderedNewestFirst(t *testing.T) {
	hermeticEnv(t)
	notes := sources.NotesFile()
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	// Written oldest first, so file order and date order disagree, and one
	// session promoted twice so its note is carried by the later record.
	var b strings.Builder
	for _, r := range []struct {
		sid, title string
		hours      int
	}{
		{"claude:s1", "oldest decision", 100},
		{"claude:s2", "middle decision", 50},
		{"claude:s3", "newest decision", 2},
		{"claude:s1", "oldest decision, promoted again", 1},
	} {
		line, err := json.Marshal(map[string]any{
			"ts":   time.Now().Add(-time.Duration(r.hours) * time.Hour).UTC().Format(time.RFC3339Nano),
			"kind": "promoted", "session": r.sid, "state": "accepted", "project": "app",
			"title": r.title, "text": "body of " + r.title,
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(notes, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := noteTitlesInPageOrder(t, string(page))
	want := []string{"oldest decision, promoted again", "newest decision", "middle decision"}
	if len(got) != len(want) {
		t.Fatalf("page carries %d notes, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("notes reach the page in this order: %q\nwant newest first: %q", got, want)
			break
		}
	}
	// The other half: the script draws that array, and a sort of its own there
	// would move the reader's newest note with the array above untouched.
	script := string(page)
	if !strings.Contains(script, "N.map(rowN)") {
		t.Errorf("the notes list is no longer drawn straight from the array — check what reorders it")
	}
	for _, reorder := range []string{"N.sort", "N.reverse", "N=N.slice().sort", "N.toSorted"} {
		if strings.Contains(script, reorder) {
			t.Errorf("the page reorders the notes with %s, so the order above is not what the reader sees", reorder)
		}
	}
}

// noteTitlesInPageOrder reads the titles out of the notes array the page hands
// its script. A session row carries "title" beside "updated" and a recall row
// has no title at all, so only a note matches; a title holding a quote arrives
// escaped and drops out of the count rather than moving in it.
func noteTitlesInPageOrder(t *testing.T, page string) []string {
	t.Helper()
	re := regexp.MustCompile(`"title":"([^"]*)","text"`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}
