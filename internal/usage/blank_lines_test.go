package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A blank line is what two processes leave when both close a truncated tail
// (#1902), and it is not corruption: the events around it are whole. Both
// readers here drop a line that does not parse, which covers it — an empty line
// is not valid JSON — but nothing said so, and a blank line in a JSON-lines
// file is exactly the kind of thing a later reader makes fatal.
func TestBlankLinesAreReadPast(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	one := func(bytes int) string {
		b, err := json.Marshal(Event{
			Time: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC), Kind: KindRecall,
			Bytes: bytes, Sessions: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := strings.Join([]string{one(100), "", one(100), "", "", one(100)}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if tot := Totals(dir); tot.Recalls != 3 || tot.Bytes != 300 {
		t.Errorf("blank lines cost events: %d recalls, %d bytes", tot.Recalls, tot.Bytes)
	}
	if imp := Impact(dir); imp.Recalls != 3 {
		t.Errorf("the impact screen counts %d of the three", imp.Recalls)
	}
	// `deja log` reads the same file through its own scanner, which keeps a
	// line on a different condition (a kind rather than a stamp), so it is
	// asserted here rather than assumed to follow.
	if evs := Events(dir, 0); len(evs) != 3 {
		t.Errorf("deja log reads %d of the three events", len(evs))
	}

	// And a record appended to such a file lands on its own line, as it would
	// on any other.
	RecordResult(dir, KindRecall, 200, 1, false)
	if tot := Totals(dir); tot.Recalls != 4 || tot.Bytes != 500 {
		body, _ := os.ReadFile(p)
		t.Errorf("appending to a log with blank lines lost something: %d recalls, %d bytes\n%s",
			tot.Recalls, tot.Bytes, body)
	}
}

// Rotation walks the same reader, so it must not choke on them either — and
// what it writes back has none, since it is written from the events.
func TestRotationSurvivesBlankLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		age := time.Duration(i%30) * 24 * time.Hour
		e := Event{Time: now.Add(-age), Kind: KindRecall, Bytes: 100, Sessions: 1,
			SessionIDs: []string{strings.Repeat("x", 200)}}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(read(p))
	rotate(p)
	after := len(read(p))
	if after == 0 || after >= before {
		t.Fatalf("rotation of a log with blank lines kept %d of %d events", after, before)
	}
	rewritten, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "\n\n") {
		t.Errorf("the rotated log carries blank lines forward")
	}
	if evs := Events(dir, 0); len(evs) != after {
		t.Errorf("deja log reads %d events from the rotated log, the counters read %d", len(evs), after)
	}
}
