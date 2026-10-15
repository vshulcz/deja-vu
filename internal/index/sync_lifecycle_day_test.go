package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func importedNoteRow(t *testing.T, dir string) SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "deja" && strings.HasPrefix(m.OrigID, "deja-note-") {
			return m
		}
	}
	t.Fatal("no imported note in the index")
	return SessionMeta{}
}

// #5053: the receiver stored the record time, which is the evidence's and in
// the sender's zone, as the day a decision was rejected. The sender shows the
// filing day in UTC. Both sides, in any zone, and a rebuild must agree on it.
func TestSyncedNoteCarriesTheFilingDayInUTC(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line := `{"ts":"2026-09-10T12:00:00Z","project":"proj","text":"cache invalidation on write made it worse","kind":"promoted","session":"claude:a18","state":"rejected","src_ts":"2026-07-20T23:45:00Z"}` + "\n"
	if err := os.WriteFile(notes, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	// The sender, fourteen hours east.
	time.Local = time.FixedZone("kiritimati", 14*3600)
	t.Setenv("DEJA_MACHINE", "east")
	a := filepath.Join(tmp, "a", "index.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "transfer")
	if _, err := ExportFull(a, out); err != nil {
		t.Fatal(err)
	}
	batches, _ := filepath.Glob(filepath.Join(out, "*.jsonl"))
	if len(batches) == 0 {
		t.Fatal("no batch written")
	}
	b, err := os.ReadFile(batches[0])
	if err != nil {
		t.Fatal(err)
	}
	var sr SyncRecord
	if err := json.Unmarshal([]byte(strings.SplitN(string(b), "\n", 2)[0]), &sr); err != nil {
		t.Fatal(err)
	}
	if sr.Lifecycle != "rejected" || sr.LifecycleAt != "2026-09-10" {
		t.Fatalf("the batch carries lifecycle %q at %q, want rejected at 2026-09-10", sr.Lifecycle, sr.LifecycleAt)
	}

	// The receiver, eleven hours west, and with no notes of its own.
	time.Local = time.FixedZone("pago", -11*3600)
	t.Setenv("DEJA_MACHINE", "west")
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "b-notes.jsonl"))
	bdir := filepath.Join(tmp, "b", "index.db")
	if _, err := Import(bdir, out); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		row := importedNoteRow(t, bdir)
		if row.Lifecycle != "rejected" || row.LifecycleAt != "2026-09-10" {
			t.Errorf("%s: lifecycle %q at %q, want rejected at 2026-09-10", when, row.Lifecycle, row.LifecycleAt)
		}
		if row.LifecycleNote != "cache invalidation on write made it worse" {
			t.Errorf("%s: note %q, want the note as filed", when, row.LifecycleNote)
		}
	}
	check("import")
	if err := Ensure(bdir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	check("rebuild")
}

// A batch from a build before the fields has only the evidence time. Its UTC
// day is stored, so peers in different zones store the same string.
func TestLegacyNoteBatchStoresTheUTCDay(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("pago", -11*3600)

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{
		Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user",
		Text: "[rejected] made it worse (from claude:a18, 2026-07-20)",
		Time: time.Date(2026, 7, 21, 13, 45, 0, 0, time.FixedZone("kiritimati", 14*3600)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	row := importedNoteRow(t, dir)
	if row.LifecycleAt != "2026-07-20" {
		t.Errorf("lifecycle_at = %q, want the UTC day 2026-07-20", row.LifecycleAt)
	}
	if row.LifecycleNote != "made it worse" {
		t.Errorf("note = %q, want it without the loader's tail", row.LifecycleNote)
	}
}
