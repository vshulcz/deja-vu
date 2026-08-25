package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeImpactLog writes events at the given ages in days, all recalls.
func writeImpactLog(t *testing.T, ages ...int) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var b strings.Builder
	for i, age := range ages {
		e := Event{
			Time: now.Add(-time.Duration(age) * 24 * time.Hour), Kind: KindRecall,
			Bytes: 100, Sessions: 1, RawBytes: 1000, SessionIDs: []string{string(rune('a' + i))},
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The impact screen reads as everything that has happened on this machine,
// while the log it counts is rewritten past 1MB keeping 14 days — so the
// figures are a window whose start moves silently, and one rotation halved
// them. `deja stats` has said which window it means since #763; this is the
// same fact on the screen that most looks like a lifetime total (#1889).
func TestImpactCarriesTheWindowItCounted(t *testing.T) {
	dir := writeImpactLog(t, 20, 10, 1)
	r := Impact(dir)
	if r.Recalls != 3 {
		t.Fatalf("counted %d recalls, want 3", r.Recalls)
	}
	if r.Since.IsZero() {
		t.Fatal("the report does not say when its window opens")
	}
	if age := time.Since(r.Since).Hours() / 24; age < 19 || age > 21 {
		t.Errorf("the window opens %.0f days ago, want the oldest event at 20", age)
	}
}

// A log with nothing in it says nothing rather than year 1 (the shape of
// #1874).
func TestAnEmptyImpactLogHasNoWindow(t *testing.T) {
	dir := writeImpactLog(t)
	r := Impact(dir)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "since") {
		t.Errorf("an empty log reports a window: %s", b)
	}
}

// And a log with events carries it in the machine form too.
func TestImpactJSONCarriesTheWindow(t *testing.T) {
	dir := writeImpactLog(t, 5, 2)
	b, err := json.Marshal(Impact(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["since"]; !ok {
		t.Errorf("--impact --json does not say which window it counted: %s", b)
	}
}
