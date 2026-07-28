package sources

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every harness writes timestamps its own way, and the ones deja gets wrong do
// not error — they land in 1970 or in year one, which sorts a session out of
// every window and makes it unreachable by any time query.
func TestHermesTimeAcceptsTheShapesItSees(t *testing.T) {
	want := time.Unix(1785238966, 0).UTC()
	for name, in := range map[string]any{
		"float seconds":  float64(1785238966),
		"json number":    json.Number("1785238966"),
		"rfc3339 string": want.Format(time.RFC3339),
	} {
		got := hermesTime(in)
		if got.IsZero() {
			t.Fatalf("%s: parsed to zero", name)
		}
		if d := got.Sub(want); d > time.Second || d < -time.Second {
			t.Fatalf("%s: got %v, want about %v", name, got.UTC(), want)
		}
	}
	for name, in := range map[string]any{
		"nil":     nil,
		"garbage": "not a time",
		"bool":    true,
	} {
		if got := hermesTime(in); !got.IsZero() {
			t.Fatalf("%s parsed to %v; a wrong time is worse than none", name, got)
		}
	}
}

// Kimi writes a message either as a plain string or as typed parts. Reading
// only one shape loses half the conversation, and reading tool parts as text
// fills recall with plumbing.
func TestKimiTextHandlesBothShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		in   any
		want string
	}{
		"plain string": {"  hello there  ", "hello there"},
		"text parts": {[]any{
			map[string]any{"type": "text", "text": "first"},
			map[string]any{"type": "text", "text": "second"},
		}, "first\nsecond"},
		"tool parts only": {[]any{
			map[string]any{"type": "tool_use", "id": "t1"},
		}, ""},
		"mixed": {[]any{
			map[string]any{"type": "tool_use", "id": "t1"},
			map[string]any{"type": "text", "text": "kept"},
		}, "kept"},
		"nil":    {nil, ""},
		"number": {float64(42), ""},
	} {
		if got := kimiText(tc.in); got != tc.want {
			t.Fatalf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// The offset parser resumes an append. Skipping too little duplicates the
// message, too much loses it, and both are silent.
func TestNotesOffsetParserResumesExactly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	first := `{"kind":"note","project":"p","text":"FIRSTNOTE","ts":"2026-01-01T00:00:00Z"}` + "\n"
	second := `{"kind":"note","project":"p","text":"SECONDNOTE","ts":"2026-01-01T00:01:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ParseNotesFileFromOffset(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if countMessages(all) != 2 {
		t.Fatalf("full parse produced %d messages", countMessages(all))
	}
	tail, err := ParseNotesFileFromOffset(path, int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if countMessages(tail) != 1 {
		t.Fatalf("resumed parse produced %d messages, want the one new note", countMessages(tail))
	}
	// Past the end there is nothing to add, and that is not an error the
	// indexer should have to special-case.
	if ss, err := ParseNotesFileFromOffset(path, 1<<20); err != nil || countMessages(ss) != 0 {
		t.Fatalf("offset past the end: %d messages, err=%v", countMessages(ss), err)
	}
	// A missing file behaves the same way.
	if _, err := ParseNotesFileFromOffset(filepath.Join(dir, "absent.jsonl"), 0); err == nil {
		t.Log("absent notes file reads as empty rather than erroring; either is fine")
	}
}

// Promoted notes carry the state and tags that decide whether recall prefers
// them, so what lands on disk has to survive a round trip.
func TestAppendPromotedTaggedWritesEveryField(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(dir, "notes.jsonl"))
	when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := AppendPromotedTagged("proj", "Title here", "the body", "claude:s1", "superseded", []string{"Retry", "retry", "#backoff"}, when); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "notes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &got); err != nil {
		t.Fatalf("note is not json: %v\n%s", err, b)
	}
	for key, want := range map[string]string{
		"kind": "promoted", "project": "proj", "title": "Title here",
		"text": "the body", "session": "claude:s1", "state": "superseded",
	} {
		if s, _ := got[key].(string); s != want {
			t.Fatalf("%s = %q, want %q", key, s, want)
		}
	}
	tags, _ := got["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("tags = %v, want the duplicate dropped and the hash stripped", tags)
	}
}

func countMessages(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		n += len(s.Messages)
	}
	return n
}
