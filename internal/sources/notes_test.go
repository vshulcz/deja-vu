package sources

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNotesPathAndAppendParse(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_NOTES_FILE", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, want := NotesFile(), filepath.Join(xdg, "deja", "notes.jsonl"); got != want {
		t.Fatalf("NotesFile=%q want %q", got, want)
	}
	path := filepath.Join(t.TempDir(), "private", "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	if err := AppendNote("app", "decision one", when); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, appendMustRead(t, path, `{"ts":"2026-01-02T03:04:05Z","project":"app","text":"decision two"}`+"\n"+`{"ts":"bad","project":"app","text":"ignored"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"other","text":"other"}`+"\n"+`{"ts":"2026-01-03T00:00:00Z","project":"app","text":"torn`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(path)
	if err != nil || len(ss) != 2 {
		t.Fatalf("notes=%#v err=%v", ss, err)
	}
	if ss[0].Project != "app" || len(ss[0].Messages) != 2 || ss[1].Project != "other" {
		t.Fatalf("grouped notes=%#v", ss)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.IndexByte(data, '\n') + 1
	if got, err := ParseNotesFileFromOffset(path, int64(offset)); err != nil || len(got) != 2 {
		t.Fatalf("offset notes=%#v err=%v", got, err)
	}
	if err := AppendNote("", "x", when); err == nil {
		t.Fatal("empty project accepted")
	}
	if err := AppendNote("app", "", when); err == nil {
		t.Fatal("empty text accepted")
	}
	if err := AppendNote("app", "  preserved  ", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Dir(path), path} {
			info, statErr := os.Stat(p)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != map[bool]os.FileMode{true: 0o600, false: 0o700}[p == path] {
				t.Fatalf("%s mode=%o", p, info.Mode().Perm())
			}
		}
	}
}

func TestNotesSymlinkAndMissingFile(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "missing", "notes.jsonl"))
	if got := LoadNotes(); got != nil {
		t.Fatalf("missing notes=%#v", got)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	t.Setenv("DEJA_NOTES_FILE", link)
	if err := AppendNote("p", "x", time.Now()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink append err=%v", err)
	}
}

func appendMustRead(t *testing.T, path, suffix string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return append(b, suffix...)
}

func TestConflictingNotes(t *testing.T) {
	a := PromotedNote{Project: "app", Session: "claude:a", State: "accepted", Title: "retry policy", Text: "use exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	b := PromotedNote{Project: "app", Session: "claude:b", State: "accepted", Title: "retry policy v2", Text: "fixed-interval retries are simpler and sufficient here", Tags: []string{"retries"}}
	c := PromotedNote{Project: "app", Session: "claude:c", State: "superseded", Title: "old retries", Text: "exponential backoff jitter outbound retries", Tags: []string{"retries"}}
	d := PromotedNote{Project: "other", Session: "claude:d", State: "accepted", Title: "retry policy", Text: "exponential backoff with jitter for outbound retries", Tags: []string{"retries"}}
	all := []PromotedNote{a, b, c, d}
	got := ConflictingNotes(a, all)
	if len(got) != 1 || got[0].Session != "claude:b" {
		t.Fatalf("conflicts = %+v", got)
	}
	// No tags: 3+ shared informative words still connect them ("retry",
	// "policy", "retries" — a genuine topical clash).
	a2, b2 := a, b
	a2.Tags, b2.Tags = nil, nil
	if got := ConflictingNotes(a2, []PromotedNote{a2, b2}); len(got) != 1 {
		t.Fatalf("topical word overlap must conflict, got %+v", got)
	}
	// A note sharing fewer than 3 informative words stays unrelated.
	far := PromotedNote{Project: "app", Session: "claude:e", State: "accepted", Title: "logging", Text: "ship structured logs with retries counter"}
	if got := ConflictingNotes(a2, []PromotedNote{a2, far}); len(got) != 0 {
		t.Fatalf("weak overlap conflicted: %+v", got)
	}
}
