package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// A history.jsonl line whose session has a rollout repeats that rollout's
// prompt. The full build has always dropped it; the per-file pass read
// history.jsonl alone and kept it, so the session was asked twice and owned by
// the history line (#4180). A line with no rollout is still the only record of
// its session and stays.
func TestCodexHistoryLeavesOutSessionsThatHaveARollout(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "10", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	withRollout := "01a0f6ce-5201-7150-9e18-9a127afed808"
	archived := "01a0f6ce-0000-7150-9e18-9a127afed808"
	alone := "01a0f6ce-9999-7150-9e18-9a127afed808"
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-10-01T12-31-51-"+withRollout+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "archived_sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "archived_sessions", "rollout-2026-09-01T10-00-00-"+archived+".jsonl.zst"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hist := filepath.Join(root, "history.jsonl")
	body := `{"session_id":"` + withRollout + `","ts":1790847117,"text":"asked in the TUI"}` + "\n" +
		`{"session_id":"` + archived + `","ts":1790847118,"text":"archived since"}` + "\n" +
		`{"session_id":"` + alone + `","ts":1790847119,"text":"rollout long gone"}` + "\n"
	if err := os.WriteFile(hist, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexHistoryFromOffset(hist, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != alone {
		var ids []string
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		t.Errorf("history sessions = %v, want only %s", ids, alone)
	}
}
