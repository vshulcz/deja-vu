package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The footer is the sentence someone reads before sharing the file, and it said
// no message text was in it while every row carried the opening line of a
// session (#2275).
func TestTheStatsPageDoesNotDenyCarryingTheTitles(t *testing.T) {
	withTempStores(t)
	dir := t.TempDir()
	page := filepath.Join(dir, "stats.html")
	if _, err := captureRun(t, "stats", "--html", page); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// The premise: the page really does embed titles taken from message text.
	if !strings.Contains(s, `"title":"`) {
		t.Fatal("the page embeds no titles, so the footer has nothing to be wrong about")
	}
	if strings.Contains(s, "No message text is included in this file") {
		t.Error("the footer denies carrying message text while the titles are message text")
	}
	if !strings.Contains(s, "opening line") {
		t.Error("the footer does not say what the file holds")
	}
}
