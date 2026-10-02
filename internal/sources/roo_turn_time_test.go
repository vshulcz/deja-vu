package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Roo writes a `ts` on every turn of api_conversation_history.json. The reader
// stamped turn N at history_item's ts plus N seconds, and that ts is when the
// task was last touched, so an hour-long task read as four seconds starting at
// its end (#4420).
func TestRooTurnsKeepTheirOwnTime(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "b0000000")
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
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(`{"id":"b0000000","ts":`+ms("09:00")+`,"task":"why does the retry loop spin"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
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
				t.Errorf("%s: %q at %s, want %s", reader.name, m.Text, m.Time.UTC().Format(time.RFC3339), w.Format(time.RFC3339))
			}
			delete(want, m.Text)
		}
		if len(want) != 0 {
			t.Errorf("%s: messages not found: %v", reader.name, want)
		}
		if !ss[0].Started.Equal(at("08:00")) {
			t.Errorf("%s: started %s, want 08:00", reader.name, ss[0].Started.UTC())
		}
	}
}
