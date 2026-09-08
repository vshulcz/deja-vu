package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Cursor writes the time of each turn into the turn, and the reader took the
// file's modification time for all of them: one real transcript here holds
// turns from July 26 and August 22 and every message was dated August 22, the
// day the file was last written (#3349).
func TestCursorTakesTheTimeFromTheTurnNotTheFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "Users-w-api", "agent-transcripts", "de875c53")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "de875c53.jsonl")
	lines := `{"role":"user","message":{"role":"user","content":[{"type":"text","text":"<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>\n<user_query>\nwhy does the retry loop drop the last attempt\n</user_query>"}]}}
{"role":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"because the counter compares with < instead of <="}]}}
{"role":"user","message":{"role":"user","content":[{"type":"text","text":"<timestamp>Saturday, Aug 22, 2026, 9:35 AM (UTC+3)</timestamp>\n<user_query>\ncap the retries at three\n</user_query>"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	// The file's own mtime is a third date, and must not be what any turn says.
	mtime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CURSOR_CLI_ROOT", root)

	ss, err := ParseCursorTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	july := time.Date(2026, 7, 26, 13, 6, 0, 0, time.FixedZone("", 3*3600))
	august := time.Date(2026, 8, 22, 9, 35, 0, 0, time.FixedZone("", 3*3600))
	if !s.Started.Equal(july) {
		t.Errorf("started = %v, want the first turn's own stamp %v", s.Started, july)
	}
	if !s.Updated.Equal(august) {
		t.Errorf("updated = %v, want the last turn's own stamp %v", s.Updated, august)
	}
	if len(s.Messages) < 3 {
		t.Fatalf("messages = %d, want at least 3", len(s.Messages))
	}
	if !s.Messages[0].Time.Equal(july) {
		t.Errorf("first turn = %v, want %v", s.Messages[0].Time, july)
	}
	if !s.Messages[1].Time.Equal(july) {
		t.Errorf("the reply carries the question's time: %v, want %v", s.Messages[1].Time, july)
	}
	if !s.Messages[2].Time.Equal(august) {
		t.Errorf("third turn = %v, want %v", s.Messages[2].Time, august)
	}
}

// The shapes the stamp can take, and the ones it cannot: a zone with minutes,
// no zone at all, a month spelled in full, and three that do not parse — a
// label other than UTC, seconds in the time, and a month name deja does not
// know. Each of those falls back to the file's modification time, which is the
// documented behaviour (#3349).
func TestCursorTurnTimeReadsTheShapesCursorWrites(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"<timestamp>Sunday, Jul 26, 2026, 1:06 PM (UTC+3)</timestamp>", "2026-07-26T13:06:00+03:00"},
		{"<timestamp>Jul 26, 2026, 1:06 PM (UTC-3:30)</timestamp>", "2026-07-26T13:06:00-03:30"},
		{"<timestamp>January 2, 2026, 9:05 AM</timestamp>", "2026-01-02T09:05:00Z"},
	} {
		got, ok := cursorTurnTime(c.in)
		if !ok {
			t.Errorf("cursorTurnTime(%q) did not parse", c.in)
			continue
		}
		if got.Format(time.RFC3339) != c.want {
			t.Errorf("cursorTurnTime(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want)
		}
	}
	for _, in := range []string{
		"<timestamp>Sunday, Jul 26, 2026, 1:06:45 PM (UTC+3)</timestamp>",
		"<timestamp>воскресенье, 26 июля 2026, 13:06</timestamp>",
		"no stamp at all, just a question",
	} {
		if _, ok := cursorTurnTime(in); ok {
			t.Errorf("cursorTurnTime(%q) parsed a shape it should have left to the file time", in)
		}
	}
}

// The encoded directory in the form the shared resolver walks: Cursor writes
// it without the leading separator, so the exported helper adds it back.
func TestCursorTranscriptProjectDirBase(t *testing.T) {
	got := CursorTranscriptProjectDirBase(filepath.Join("root", "projects", "Users-me-work-app", "agent-transcripts", "s1", "s1.jsonl"))
	if got != "-Users-me-work-app" {
		t.Errorf("CursorTranscriptProjectDirBase = %q, want %q", got, "-Users-me-work-app")
	}
	if got := CursorTranscriptProjectDirBase(filepath.Join("agent-transcripts", "s1.jsonl")); got != "" {
		t.Errorf("a path with no projects directory above it = %q, want empty", got)
	}
}
