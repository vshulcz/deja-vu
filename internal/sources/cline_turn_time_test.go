package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Cline's legacy extension stamps `ts: Date.now()` on the user turns and the
// completed assistant turns it appends to api_conversation_history.json, and
// taskHistory.json's ts is when the task was last touched. The roo fix (#4420)
// for the same store shape.
func TestClineLegacyTurnsKeepTheirOwnTime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tasks", "1759300000000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := func(hm string) time.Time {
		v, err := time.Parse(time.RFC3339, "2026-10-01T"+hm+":00Z")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ms := func(hm string) string { return itoa64(at(hm).UnixMilli()) }
	body := `[
	 {"role":"user","ts":` + ms("08:00") + `,"content":[{"type":"text","text":"<task>\nwhy does the retry loop spin\n</task>"}]},
	 {"role":"assistant","ts":` + ms("08:10") + `,"content":[{"type":"text","text":"Checking the loop."},{"type":"tool_use","id":"t1","name":"execute_command","input":{"command":"go test ./retry/..."}}]},
	 {"role":"user","ts":` + ms("08:20") + `,"content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL retry_test.go:12"}]},
	 {"role":"assistant","content":[{"type":"text","text":"No ts on this one."}]},
	 {"role":"assistant","ts":` + ms("09:00") + `,"content":[{"type":"text","text":"The cap was never applied."}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	history := `[{"id":"1759300000000","ts":` + ms("09:00") + `,"task":"why does the retry loop spin"}]`
	if err := os.WriteFile(filepath.Join(root, "state", "taskHistory.json"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClineFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	want := map[string]time.Time{
		"why does the retry loop spin": at("08:00"),
		"Checking the loop.":           at("08:10"),
		"$ go test ./retry/...":        at("08:10"),
		"FAIL retry_test.go:12":        at("08:20"),
		// A turn without its own ts keeps the old scheme.
		"No ts on this one.":         at("09:00").Add(3 * time.Second),
		"The cap was never applied.": at("09:00"),
	}
	for _, m := range ss[0].Messages {
		w, ok := want[m.Text]
		if !ok {
			continue
		}
		if !m.Time.Equal(w) {
			t.Errorf("%q at %s, want %s", m.Text, m.Time.UTC().Format(time.RFC3339), w.Format(time.RFC3339))
		}
		delete(want, m.Text)
	}
	if len(want) != 0 {
		t.Errorf("messages not found: %v", want)
	}
	if !ss[0].Started.Equal(at("08:00")) {
		t.Errorf("started %s, want 08:00", ss[0].Started.UTC())
	}
}
