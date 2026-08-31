package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A note stamped next year sits at the top of `deja last` and stays there: deja
// cannot tell a skewed clock from a deliberate date, so the ordering is left
// alone — but the reader is not told either, and a typo'd year or a millisecond
// stamp read as seconds looks like nothing at all (#2063).
func TestDoctorNamesASessionStampedInTheFuture(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	next := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339)
	now := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := `{"ts":"` + next + `","project":"tmp/app","text":"a stray note stamped next year"}` + "\n" +
		`{"ts":"` + now + `","project":"tmp/app","text":"the pgbouncer pool times out under load"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stamped in the future") {
		t.Errorf("doctor says nothing about a session dated ahead of the clock:\n%s", out.String())
	}

	// And it stays quiet when nothing is: the line is a finding, not furniture.
	if err := os.WriteFile(notes, []byte(`{"ts":"`+now+`","project":"tmp/app","text":"the pgbouncer pool times out under load"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(&out, []string{"--offline"}, stubLookup("1.0.0", false), dir); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "stamped in the future") {
		t.Errorf("doctor reports a future stamp with none in the store:\n%s", out.String())
	}
}
