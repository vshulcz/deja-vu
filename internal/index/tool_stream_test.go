package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEachToolOutputStreamsOnlyToolRecords(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-stream")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var got []Record
	var harness string
	if err := EachToolOutput(dir, func(m SessionMeta, r Record) {
		got = append(got, r)
		harness = m.Harness
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one tool-output record, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "shellcheck") {
		t.Fatalf("wrong record: %q", got[0].Text)
	}
	// The session is what makes the record countable — a caller clustering
	// errors needs to know which session and which harness hit it.
	if harness != "claude" {
		t.Fatalf("harness = %q", harness)
	}
	if got[0].Time.IsZero() {
		t.Fatal("a record with no time cannot be dated on a report")
	}
}

func TestEachToolOutputOnMissingStore(t *testing.T) {
	if err := EachToolOutput(filepath.Join(t.TempDir(), "nope"), func(SessionMeta, Record) {}); err == nil {
		t.Fatal("a missing store should report an error")
	}
}

// recordsForKey skips the body of records belonging to other sessions. It must
// still return exactly what a full decode of the log would have returned for
// this one — the shortcut is a decode it avoids, not a record it drops.
func TestRecordsForKeyMatchesAFullScan(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "claude", "-tmp-keys")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"k1", "k2"} {
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first turn in ` + sid + `"}}`,
			`{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer for ` + sid + `"}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records.bin")
	tbl := tablesFromManifest(m)
	for key := range m.Sessions {
		var want []Record
		if err := eachRecord(path, tbl, func(r Record) {
			if r.Key == key {
				want = append(want, r)
			}
		}); err != nil {
			t.Fatal(err)
		}
		got, err := recordsForKey(path, tbl, key)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) || len(want) == 0 {
			t.Fatalf("%s: got %d records, full scan found %d", key, len(got), len(want))
		}
		for i := range want {
			if got[i].Text != want[i].Text || got[i].Role != want[i].Role || !got[i].Time.Equal(want[i].Time) {
				t.Fatalf("%s record %d differs: %#v vs %#v", key, i, got[i], want[i])
			}
		}
	}
}
