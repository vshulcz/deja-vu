package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// oneLine writes a log holding exactly the given line.
func oneLine(t *testing.T, line string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	p := Path(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The counters kept a line with a stamp; `deja log` kept one with a kind. So a
// half-written line was a row on one surface and nothing on the other — and a
// line with no kind, which no counter has a case for, still set `since`, the
// window every figure on the impact screen is measured from (#1917).
func TestBothReadersAgreeOnWhatAnEventIs(t *testing.T) {
	for _, c := range []struct {
		name, line string
		kept       bool
	}{
		{"whole", `{"t":"2026-08-20T10:00:00Z","kind":"recall","bytes":100,"sessions":1}`, true},
		{"unknown kind", `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100}`, true},
		{"no stamp", `{"kind":"recall","bytes":100,"sessions":1}`, false},
		{"no kind", `{"t":"2026-08-20T10:00:00Z","bytes":100,"sessions":1}`, false},
		{"neither", `{"bytes":100,"sessions":1}`, false},
	} {
		dir := oneLine(t, c.line)
		byCounters := len(read(Path(dir)))
		byLog := len(Events(dir, 0))
		if byCounters != byLog {
			t.Errorf("%s: the counters read %d and deja log reads %d", c.name, byCounters, byLog)
		}
		want := 0
		if c.kept {
			want = 1
		}
		if byCounters != want {
			t.Errorf("%s: kept %d, want %d", c.name, byCounters, want)
		}
	}
}

// A line no counter can count must not decide the period the counts cover.
func TestALineWithNoKindDoesNotMoveTheWindow(t *testing.T) {
	dir := oneLine(t, `{"t":"2020-01-01T00:00:00Z","bytes":100,"sessions":1}`)
	if tot := Totals(dir); !tot.Since.IsZero() {
		t.Errorf("a kindless line pulled the window back to %s", tot.Since.Format("2006-01-02"))
	}
	if imp := Impact(dir); !imp.Since.IsZero() {
		t.Errorf("the impact screen dates its counts from %s", imp.Since.Format("2006-01-02"))
	}
}

// An unknown kind is a different thing: deja may have written it, an older or
// newer version of itself, so it stays an event even though nothing counts it.
func TestAnUnknownKindStaysAnEvent(t *testing.T) {
	dir := oneLine(t, `{"t":"2026-08-20T10:00:00Z","kind":"weather","bytes":100}`)
	if got := len(Events(dir, 0)); got != 1 {
		t.Errorf("deja log hides an event it does not recognise: %d", got)
	}
	if tot := Totals(dir); tot.Since.IsZero() {
		t.Error("an unknown kind stopped counting towards the window")
	}
}
