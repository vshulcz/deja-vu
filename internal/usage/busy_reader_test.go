package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Since #2472 the reader's own searches land in the log too, and someone who
// lives in the CLI writes far more of them than the machine writes injections.
// The window that rotation keeps is a window in time, not a count, so the
// searches must not crowd a fortnight of session starts out of what the impact
// screen reports.
func TestASearchFloodDoesNotEvictTheFortnightOfStarts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	path := Path(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var lines []string
	starts := 0
	for day := 13; day >= 0; day-- {
		for k := 0; k < 2; k++ {
			e := Event{Time: now.Add(-time.Duration(day)*24*time.Hour - time.Duration(k)*time.Hour),
				Kind: KindHook, Bytes: 1800, Sessions: 3, RawBytes: 21000}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
			starts++
		}
		for i := 0; i < 1000; i++ {
			e := Event{Time: now.Add(-time.Duration(day)*24*time.Hour - time.Duration(i)*time.Second),
				Kind: KindSearch, Bytes: 5200, Sessions: 12}
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(b))
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < rotateAt {
		t.Fatalf("the fixture is meant to be over the rotation size; it is %d bytes", fi.Size())
	}
	rotate(path)

	imp := Impact(dir)
	if imp.Injections != starts {
		t.Errorf("impact reports %d session starts of the %d in the log", imp.Injections, starts)
	}
	if imp.Recalls != 0 {
		t.Errorf("searches are counted as recalls served: %d", imp.Recalls)
	}
	if got := Totals(dir); got.Injections != starts {
		t.Errorf("stats reports %d session starts of %d", got.Injections, starts)
	}
	// The oldest event decides the period every count on that screen covers.
	if age := time.Since(imp.Since); age < 13*24*time.Hour {
		t.Errorf("the reported period covers only %s, so the oldest starts were dropped", age.Round(time.Hour))
	}
}
