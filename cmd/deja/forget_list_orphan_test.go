package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A tombstone whose session exists nowhere suppresses nothing. This machine
// carried three left by a stand run months ago, and `forget --list` printed
// them exactly like a session someone forgot on purpose yesterday (#3753).
func TestForgetListMarksATombstoneWhoseSessionIsGone(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid string) string {
		t.Helper()
		p := filepath.Join(store, name)
		rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"` + sid + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(p, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("kept.jsonl", "kept-one")
	gone := write("gone.jsonl", "gone-one")
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"kept-one", "gone-one"} {
		if _, err := captureRunStderr(t, "forget", "--session", sid); err != nil {
			t.Fatal(err)
		}
	}
	// One transcript is deleted on disk after it was forgotten; the other is
	// still there, and its tombstone is the only thing keeping it out of search.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		id, _, _ := strings.Cut(line, "\t")
		rows[id] = line
	}

	if got := rows["claude:gone-one"]; !strings.Contains(got, "suppresses nothing") {
		t.Errorf("the tombstone of a deleted session is not marked: %q", got)
	}
	// Still standing between a transcript and search: that row is unchanged.
	if got := rows["claude:kept-one"]; got != "claude:kept-one" {
		t.Errorf("a tombstone still hiding a transcript was marked: %q", got)
	}
	// Still one line per tombstone, each starting with its id — `wc -l` and
	// `cut -f1` read the list the way they did before.
	if len(rows) != 2 {
		t.Errorf("want one row per tombstone, got %d:\n%s", len(rows), out)
	}
}

// Nothing forgotten whose session is gone: the list is byte-for-byte what it
// was, so a machine with no leftovers sees no change at all.
func TestForgetListIsUnchangedWithNoOrphans(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-10T10:00:00Z","sessionId":"ab12-one","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab12"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "forget", "--list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "claude:ab12-one" {
		t.Errorf("list = %q, want only the id", out)
	}
}
