package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The text report says what arrived from a machine there is no peer row for
// (#2379); `doctor --json` said nothing, so a tool watching sync saw a machine
// that had never exchanged anything (#2382).
func TestDoctorJSONReportsWhatArrivedWithoutAPeerRow(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	importFrom := func(machine string, ids ...string) {
		t.Helper()
		batch := t.TempDir()
		var lines []string
		for _, id := range ids {
			lines = append(lines, fmt.Sprintf(
				`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
					`"text":"work from %s","time":%q,"origin":%q}`,
				id, machine, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), machine))
		}
		if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runSync(dir, []string{"import", batch}); err != nil {
			t.Fatal(err)
		}
	}
	importFrom("laptop.local", "a0", "a1", "a2")
	importFrom("desktop", "b0", "b1")

	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers    []map[string]any `json:"peers"`
			Imported []struct {
				Machine  string `json:"machine"`
				Sessions int    `json:"sessions"`
			} `json:"imported"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	if len(report.Sync.Peers) != 0 {
		t.Fatalf("a peer row appeared from nowhere: %+v", report.Sync.Peers)
	}
	got := map[string]int{}
	for _, im := range report.Sync.Imported {
		got[im.Machine] = im.Sessions
	}
	if got["laptop.local"] != 3 || got["desktop"] != 2 {
		t.Errorf("the machines that sent work are missing or miscounted: %+v", report.Sync.Imported)
	}

	// A machine that has exchanged nothing says nothing rather than an empty
	// list, so a reader can tell it from a deja too old to report this.
	hermeticEnv(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	raw, err = captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"imported"`) {
		t.Errorf("a store with no imports carries an imported key:\n%s", raw)
	}
}
