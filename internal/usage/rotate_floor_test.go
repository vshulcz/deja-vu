package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agedLog writes a log past the rotation size whose events are all the given
// number of days old.
func agedLog(t *testing.T, days int) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	path = Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		e := Event{
			Time: now.Add(-time.Duration(days+i%5) * 24 * time.Hour), Kind: KindRecall,
			Bytes: 500, Sessions: 2, RawBytes: 5000, SessionIDs: []string{strings.Repeat("x", 200)},
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// Rotation drops what is older than the keep window, and when everything is
// older than it — a fortnight away from the machine — that used to be the whole
// file. The first recall on return left zero bytes, and `deja stats --impact`
// said "no recall activity recorded yet" about a log that had held four
// thousand events (#1922). Bounding the file is the job; erasing the record is
// not.
func TestRotationLeavesTheNewestEventsWhenAllAreOld(t *testing.T) {
	dir, p := agedLog(t, 21)
	before := len(read(p))
	if before == 0 {
		t.Fatal("the fixture wrote nothing")
	}

	rotate(p)

	kept := read(p)
	if len(kept) == 0 {
		t.Fatalf("rotation left nothing of %d events", before)
	}
	if len(kept) >= before {
		t.Errorf("rotation kept %d of %d — it is meant to bound the file", len(kept), before)
	}
	if tot := Totals(dir); tot.Since.IsZero() {
		t.Error("the window says the log holds nothing, though it holds the newest events")
	}
	if imp := Impact(dir); imp.Recalls == 0 {
		t.Error("the impact screen reports no activity after a rotation that kept events")
	}
	// The newest ones, not an arbitrary slice: the last thing that happened is
	// what a reader wants when the rest has aged out.
	newest := kept[0].Time
	for _, e := range kept {
		if e.Time.After(newest) {
			newest = e.Time
		}
	}
	if age := time.Since(newest).Hours() / 24; age > 22 {
		t.Errorf("the newest kept event is %.0f days old, so the oldest were kept instead", age)
	}
}

// The ordinary case is unchanged: with events inside the window, only those
// stay.
func TestRotationStillDropsWhatIsOutsideTheWindow(t *testing.T) {
	dir, p := agedLog(t, 0)
	// Half of them well outside the window.
	old := Event{Time: time.Now().UTC().Add(-40 * 24 * time.Hour), Kind: KindRecall, Bytes: 1, Sessions: 1}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	rotate(p)

	for _, e := range read(p) {
		if time.Since(e.Time) > 15*24*time.Hour {
			t.Errorf("an event %s old survived a rotation with fresh events in the file", time.Since(e.Time))
			break
		}
	}
	if tot := Totals(dir); tot.Recalls == 0 {
		t.Error("the fresh events were dropped too")
	}
}
