package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A machine holding only work that arrived by sync used to report "exported 0
// records" while its own `stats` showed hundreds of messages, which reads as
// data loss rather than as a rule (#2962). It now names what it is holding and
// the flag that sends it.
func TestSyncExportSaysWhatItHoldsBack(t *testing.T) {
	home := hermeticEnv(t)
	dir := filepath.Join(home, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)

	batch := filepath.Join(home, "from-elsewhere")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := `{"harness":"claude","session_id":"far1","project":"app","role":"user",` +
		`"text":"relayneedle the loader stalls on boot","time":"` + at + `","origin":"container-1"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := index.Import(dir, batch); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	out := filepath.Join(home, "outgoing")
	stdout := captureStdout(t, func() {
		if err := runSync(dir, []string{"export", out, "--full"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stdout, "arrived from other machines") {
		t.Errorf("an empty export said nothing about what it holds:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--include-imported") {
		t.Errorf("the reader is not told how to send it:\n%s", stdout)
	}

	// And the flag sends it, under the name of the machine the work happened
	// on rather than this one.
	relayOut := filepath.Join(home, "relayed")
	if err := runSync(dir, []string{"export", relayOut, "--include-imported"}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(relayOut, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("nothing was written: %v", err)
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var sr struct {
		Origin  string `json:"origin"`
		Project string `json:"project"`
		Text    string `json:"text"`
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(string(body)), "\n")[0])
	if err := json.Unmarshal([]byte(line), &sr); err != nil {
		t.Fatal(err)
	}
	if sr.Origin != "container-1" {
		t.Errorf("relayed record signed %q, want the machine the work happened on", sr.Origin)
	}
	if sr.Project != "app" {
		t.Errorf("project = %q, want the original name", sr.Project)
	}
	if !strings.Contains(sr.Text, "relayneedle") {
		t.Errorf("the work itself did not travel: %q", sr.Text)
	}
}
