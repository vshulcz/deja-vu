package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A note's bucket is labelled by the day its id names (#883) while sessions are
// dated in the reader's zone (#849). Minting the bucket in UTC put the two
// calendars in one list: east of UTC a note written a quarter of an hour after
// a session was dated the day before it (#911).
func TestNoteBucketFollowsTheReadersDay(t *testing.T) {
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
		t.Fatalf("got %d note sessions, want 1", len(ss))
	}
	// 23:45 UTC is 13:45 the next day in Kiritimati: that is the day the
	// reader's other lines carry, so it is the day the bucket carries.
	if want := "deja-2026-07-21-tz"; ss[0].ID != want {
		t.Errorf("bucket id = %q, want %q", ss[0].ID, want)
	}
}
