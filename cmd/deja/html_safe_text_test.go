package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"
)

// hostile is what a transcript or a hand-written note can put in a name:
// markup, an escape byte and a bidi override, which reverses the rendering of
// everything after it.
const hostile = "</script><img src=x onerror=alert(1)> \x1b[31m & <b>bold\u202e"

// Every terminal row deja prints scrubs these; the pages did not, and a page is
// where a bidi override actually reverses a line for a reader (#2090).
func TestTheStatsPageDoesNotCarryControlOrBidiCharacters(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s1", Project: hostile, Title: hostile,
		Started:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Updated:  time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC),
		Messages: []model.Message{{Role: "user", Text: hostile}},
	}
	report := stats.Report{
		TotalSessions: 1, TotalMessages: 1,
		Longest:     stats.SessionStat{ID: "s1", Messages: 1, Harness: "claude", Project: hostile, Title: hostile},
		Harnesses:   []stats.HarnessStats{{Harness: "claude", Sessions: 1, Messages: 1}},
		TopProjects: []stats.ProjectStats{{Project: hostile, Sessions: 1}},
	}
	page, err := newStatsHTMLPage(report, []model.Session{s})
	if err != nil {
		t.Fatal(err)
	}
	js := string(page.SessionsJSON)
	// The premise: the row reached the page, so this is about what it carried
	// rather than about an empty table.
	if !strings.Contains(js, "bold") {
		t.Fatalf("the session did not reach the page at all: %s", js)
	}
	assertNoControlOrBidi(t, "the stats page", js)
}

// `deja view` is the browsing page, and a promoted note is text a person wrote,
// so it reaches the page the way a transcript's title does.
func TestTheViewPageDoesNotCarryControlOrBidiCharacters(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted",
		"title":   "pool sizing " + hostile,
		"text":    "the pool was exhausted\n" + hostile,
		"project": "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, err := captureRun(t, "view", "--no-open", "--out", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if !strings.Contains(page, "pool sizing") {
		t.Fatalf("the note did not reach the page at all")
	}
	assertNoControlOrBidi(t, "the view page", page)
}

func assertNoControlOrBidi(t *testing.T, what, s string) {
	t.Helper()
	for _, bad := range []struct{ name, text string }{
		{"an escape byte", "\x1b"},
		{"a bidi override", "\u202e"},
		{"a carriage return", "\r"},
	} {
		if strings.Contains(s, bad.text) {
			t.Errorf("%s carries %s", what, bad.name)
		}
	}
}
