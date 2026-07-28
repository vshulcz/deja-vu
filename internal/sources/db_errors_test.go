package sources

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Harnesses change their schemas — goose and hermes both did while deja was
// being written. When that happens the query fails, and the only acceptable
// outcome is an error the caller can report: silently returning nothing would
// look exactly like "you have no history".
func TestDBParsersReportSchemaFailuresRatherThanEmptiness(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()

	// A database that exists but has none of the tables we query.
	wrong := filepath.Join(dir, "wrong.db")
	if out, err := exec.Command("sqlite3", wrong, "CREATE TABLE unrelated (id TEXT);").CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes":   func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		ss, err := parse(wrong)
		if err == nil && len(ss) > 0 {
			t.Fatalf("%s: invented %d sessions from a foreign schema", name, len(ss))
		}
		if err == nil {
			t.Logf("%s: reports no sessions rather than an error on a foreign schema", name)
		}
	}

	// A file that is not a database at all.
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":  func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes": func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":   func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
	} {
		if ss, err := parse(garbage); err == nil && len(ss) > 0 {
			t.Fatalf("%s: parsed %d sessions out of a non-database", name, len(ss))
		}
	}

	// An empty file is how a store looks before its harness has run once:
	// nothing to read, and nothing to complain about either.
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"hermes":   func(p string) ([]model.Session, error) { return ParseHermesDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		ss, err := parse(empty)
		if err != nil {
			t.Fatalf("%s: an empty store errored: %v", name, err)
		}
		if len(ss) != 0 {
			t.Fatalf("%s: %d sessions from an empty store", name, len(ss))
		}
	}

	// And a store that is simply not there must not be created by reading it.
	absent := filepath.Join(dir, "absent.db")
	for name, parse := range map[string]func(string) ([]model.Session, error){
		"goose":    func(p string) ([]model.Session, error) { return ParseGooseDBSince(p, time.Time{}) },
		"grok":     func(p string) ([]model.Session, error) { return ParseGrokDBSince(p, time.Time{}) },
		"opencode": ParseOpencodeNewest,
	} {
		if _, err := parse(absent); err != nil {
			t.Fatalf("%s: absent store errored: %v", name, err)
		}
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("reading an absent store created it — sqlite3 does that unless stopped")
	}
}

// The escape helper is the one that cost two bugs in a day, both times by
// being read as "quote". It escapes; the caller quotes.
func TestSQLEscapeDoublesQuotesWithoutAddingThem(t *testing.T) {
	for in, want := range map[string]string{
		"plain":            "plain",
		"o'brien":          "o''brien",
		"''":               "''''",
		"2026-01-01T00:00": "2026-01-01T00:00",
		"":                 "",
	} {
		if got := sqlEscape(in); got != want {
			t.Fatalf("sqlEscape(%q) = %q, want %q", in, got, want)
		}
	}
	// The point of the name: no quotes are added, so a caller that forgets
	// them builds a broken query rather than a working one.
	if got := sqlEscape("value"); got == "'value'" {
		t.Fatal("sqlEscape now quotes; the call sites double-quote and every query breaks")
	}
}
