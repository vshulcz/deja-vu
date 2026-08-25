package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// oneEvent writes a log holding a single event of the given kind.
func oneEvent(t *testing.T, kind string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	e := Event{
		Time: time.Now().UTC().Add(-time.Hour), Kind: kind,
		Bytes: 100, Sessions: 1, RawBytes: 1000, SessionIDs: []string{"s1"},
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// What deja served an agent is one question, and the two screens that answer it
// walked the log with different rules: a blame was a recall to `deja stats` and
// nothing to `deja stats --impact`, so its bytes were missing from the ratio the
// impact screen prints (#1907). #1569 aligned five counters here; Impact was not
// among them.
func TestTheTwoScreensCountTheSameServings(t *testing.T) {
	for _, kind := range []string{KindRecall, KindContext, KindBlame, KindResource} {
		dir := oneEvent(t, kind)
		tot := Totals(dir)
		imp := Impact(dir)
		if tot.Recalls != 1 {
			t.Errorf("%s: stats counts %d recalls, want 1", kind, tot.Recalls)
		}
		if imp.Recalls != 1 {
			t.Errorf("%s: impact counts %d recalls, want 1", kind, imp.Recalls)
		}
		if imp.ServedBytes != 100 || imp.RawBytes != 1000 {
			t.Errorf("%s: impact served %d of %d raw, want 100 of 1000 — the distilled ratio is short by it",
				kind, imp.ServedBytes, imp.RawBytes)
		}
	}
}

// A read of deja://session/… hands the agent a whole session, which is why the
// kind exists — and nothing counted it at all.
func TestAResourceReadIsCountedAsServed(t *testing.T) {
	dir := oneEvent(t, KindResource)
	if tot := Totals(dir); tot.Recalls != 1 || tot.Bytes != 100 {
		t.Errorf("stats counts a resource read as %d recalls, %d bytes", tot.Recalls, tot.Bytes)
	}
}

// The kinds that write rather than serve stay uncounted on both screens.
func TestWritingKindsAreNotServings(t *testing.T) {
	for _, kind := range []string{KindRemember, KindSearch, KindHandoff} {
		dir := oneEvent(t, kind)
		if tot := Totals(dir); tot.Recalls != 0 || tot.Injections != 0 {
			t.Errorf("%s: stats counts it as served: %#v", kind, tot)
		}
		if imp := Impact(dir); imp.Recalls != 0 || imp.Injections != 0 {
			t.Errorf("%s: impact counts it as served: %#v", kind, imp)
		}
	}
}
