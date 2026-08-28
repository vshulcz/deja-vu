package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doctor's ingest line ends "see `deja doctor --json`", and the JSON carried
// the same two numbers the line had just printed: a person told a line was
// skipped could not learn which file it was in. The manifest has held that per
// file since #2015 — nothing exposed it (#2189).
func TestDoctorJSONNamesTheFilesItsCountsCameFrom(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []string{
		`{"ts":"` + now + `","text":"the migration held the lock"}`,
		`not json at all`,
		`{"ts":"2026-01-03","text":"a date without a time"}`,
	}
	if err := os.WriteFile(notes, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	raw, _ := captureRun(t, "doctor", "--json")
	var report struct {
		Ingest map[string]struct {
			MalformedLines int `json:"malformed_lines"`
		} `json:"ingest_health"`
		Files map[string]struct {
			Malformed int    `json:"malformed,omitempty"`
			Error     string `json:"error,omitempty"`
			Clipped   int    `json:"clipped,omitempty"`
		} `json:"ingest_files"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json does not parse: %v", err)
	}
	// The premise: this run refused lines, so a report naming no file is
	// hiding something rather than having nothing to say.
	if report.Ingest["deja"].MalformedLines != 2 {
		t.Fatalf("the run did not refuse the two lines, so this measures nothing:\n%s", raw)
	}
	if len(report.Files) == 0 {
		t.Fatalf("doctor --json names no file for the lines it says were skipped:\n%s", raw)
	}
	got, ok := report.Files[notes]
	if !ok {
		named := make([]string, 0, len(report.Files))
		for p := range report.Files {
			named = append(named, p)
		}
		t.Fatalf("the notes file is not among the files named (%v); a person fixing it is not told it is theirs", named)
	}
	if got.Malformed != 2 {
		t.Errorf("the notes file is reported with %d refused lines, want 2", got.Malformed)
	}
	// And here, where every file belongs to a harness deja knows, the per-file
	// counts add up to the rollup printed above them — or the reader has two
	// numbers and no way to tell which one is wrong.
	sum := 0
	for _, f := range report.Files {
		sum += f.Malformed
	}
	total := 0
	for _, h := range report.Ingest {
		total += h.MalformedLines
	}
	if sum != total {
		t.Errorf("the files account for %d refused lines and the rollup says %d", sum, total)
	}
}
