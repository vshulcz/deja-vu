package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A Claude session started from a home directory takes its project from the
// files it worked on. An appended tail with no files of its own names only the
// home directory, and that must not replace what the whole file said.
func TestAppendedTailKeepsTheProjectFromPaths(t *testing.T) {
	root, dir := allHarnessEnv(t)
	repo := filepath.Join(root, "code", "alpha")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "claude", "-home-me", "s.jsonl")
	head := claudeLineAt("s1", "2026-07-17T09:00:00Z", "projneedle look at alpha", "/home/me")
	for i, f := range []string{"a.go", "b.go", "c.go"} {
		head += claudeEditAt("s1", "2026-07-17T09:00:0"+string(rune('1'+i))+"Z", filepath.Join(repo, f), "/home/me")
	}
	write(t, p, head)
	o := search.Options{Query: "projneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	appendTo(t, p, claudeLineAt("s1", "2026-07-17T09:05:00Z", "projneedle now explain it", "/home/me"))
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	if err := EnsureForSearch(rebuilt, o, true, nil); err != nil {
		t.Fatal(err)
	}
	project := func(dir string) string {
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["claude:s1"].Project
	}
	if got, want := project(dir), project(rebuilt); got != want || !strings.HasSuffix(want, "alpha") {
		t.Errorf("after the append the project is %q, a rebuild says %q", got, want)
	}
}

// A last line that parses but has no newline yet is indexed; the next pass
// must not read it again when the writer finishes the line and appends more.
func TestUnterminatedLastLineIsIndexedOnce(t *testing.T) {
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	first := claudeLine("s1", "2026-01-02T03:04:05Z", "dupneedle first")
	second := strings.TrimSuffix(claudeLine("s1", "2026-01-02T03:04:06Z", "dupneedle second"), "\n")
	write(t, p, first+second)

	o := search.Options{Query: "dupneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("baseline: %#v err=%v", ss, err)
	}
	appendTo(t, p, "\n"+claudeLine("s1", "2026-01-02T03:04:07Z", "dupneedle third"))
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "dupneedle", All: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	if len(texts) != 3 {
		t.Fatalf("after append: %d messages %q, want 3 (first, second, third once each)", len(texts), texts)
	}
}
