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

// Notes are grouped by the reader's day (#911), so a laptop that changed zones
// regroups them on the next rebuild. Nothing is lost, but an id that was shared
// or pasted somewhere keeps resolving and starts naming a different note, and
// nothing said the days had moved (#935).
func TestDoctorSaysWhenNoteDaysRegrouped(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	// 09:30 and 23:30 on the same day in UTC+3 — one bucket there, two once
	// the machine is west of UTC.
	body := `{"ts":"2026-08-02T06:30:00Z","project":"t","text":"morning: the pool cap is 20"}` + "\n" +
		`{"ts":"2026-08-02T20:30:00Z","project":"t","text":"late: the ticker window stays at 30s"}` + "\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_NOTES_FILE", notes)

	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	time.Local = time.FixedZone("east", 3*60*60)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	doctorHarnesses(&out, dir)
	if strings.Contains(out.String(), "regrouped") {
		t.Errorf("an index read in the zone it was built in complained:\n%s", out.String())
	}

	// The same index, now read seven hours west: the morning note belongs to
	// the previous day here.
	time.Local = time.FixedZone("west", -7*60*60)
	out.Reset()
	doctorHarnesses(&out, dir)
	if !strings.Contains(out.String(), "regrouped") {
		t.Errorf("nothing said the days moved:\n%s", out.String())
	}

	// A note written since the build is growth, not regrouping: it must not
	// raise the warning.
	time.Local = time.FixedZone("east", 3*60*60)
	if err := os.WriteFile(notes, []byte(body+`{"ts":"2026-08-03T06:30:00Z","project":"t","text":"today: rotate the certificate"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHarnesses(&out, dir)
	if strings.Contains(out.String(), "regrouped") {
		t.Errorf("a note added since the build was called a regrouping:\n%s", out.String())
	}
}
