package sources

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ARCHITECTURE.md is where a reader goes to learn which day a `remember` lands
// on. It said UTC calendar day, which stopped being true in #911 — east of UTC
// the bucket carries the reader's day. The doc kept promising the old zone, so
// this derives the zone from the grouping itself and holds the sentence to it.
func TestArchitectureNamesTheZoneNotesAreGroupedIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	line := `{"ts":"2026-07-20T23:45:00Z","project":"tz","text":"the anemometer drifted"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", path)

	zone, err := time.LoadLocation("Pacific/Kiritimati") // UTC+14
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	saved := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = saved })

	ss := LoadNotes()
	if len(ss) != 1 {
		t.Fatalf("got %d note sessions, want the 1 this case measures", len(ss))
	}
	groupedInUTC := strings.Contains(ss[0].ID, "2026-07-20")

	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	sentence := regexp.MustCompile(`(?s)Notes are grouped into[^.]*\.`)
	m := sentence.Find([]byte(strings.Join(strings.Fields(string(doc)), " ")))
	if m == nil {
		t.Fatal("ARCHITECTURE.md no longer says how notes are grouped; a reader has nowhere else to learn the day")
	}
	claimsUTC := strings.Contains(string(m), "UTC")
	if claimsUTC != groupedInUTC {
		t.Errorf("bucket id %q was minted in %s, but ARCHITECTURE.md says: %s",
			ss[0].ID, map[bool]string{true: "UTC", false: "the reader's zone"}[groupedInUTC], m)
	}
}
