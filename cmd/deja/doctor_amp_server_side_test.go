package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Amp builds from 2026-03-31 on keep threads on ampcode.com and write nothing
// under threads/, while the data directory still holds bin, logs and pids. The
// row said `missing` with no reason, which reads as "you never used Amp"
// (#4355).
func TestDoctorSaysCurrentAmpKeepsThreadsServerSide(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	dataDir := filepath.Join(tmp, "data", "amp")

	doctorAmp := func() (string, doctorStore) {
		t.Helper()
		var buf bytes.Buffer
		if err := runDoctor(&buf, []string{"--offline"}, stubLookup("1.0.0", false), t.TempDir()); err != nil {
			t.Fatal(err)
		}
		store, _ := inspectDoctorStore(doctorCheckNamed(t, "amp"))
		return harnessRow(t, buf.String(), "amp"), store
	}

	// Control: no Amp on the machine, no sentence about it.
	if row, store := doctorAmp(); strings.Contains(row, "ampcode.com") || store.Note != "" {
		t.Fatalf("row = %q, note = %q — nothing says Amp is installed here", row, store.Note)
	}

	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	row, store := doctorAmp()
	if !strings.Contains(row, "missing") || !strings.Contains(row, "ampcode.com") {
		t.Fatalf("row = %q — want missing plus where current Amp keeps its threads", row)
	}
	if !strings.Contains(store.Note, "ampcode.com") {
		t.Fatalf("json note = %q — want the same reason as the text row", store.Note)
	}

	// A thread on disk is a store that reads, and the note goes.
	threads := filepath.Join(dataDir, "threads")
	if err := os.MkdirAll(threads, 0o755); err != nil {
		t.Fatal(err)
	}
	thread := `{"id":"T-1","created":1774950000000,"title":"x","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	if err := os.WriteFile(filepath.Join(threads, "T-1.json"), []byte(thread), 0o644); err != nil {
		t.Fatal(err)
	}
	if row, store := doctorAmp(); strings.Contains(row, "ampcode.com") || store.Note != "" {
		t.Fatalf("row = %q, note = %q — a store with threads needs no note", row, store.Note)
	}
}
