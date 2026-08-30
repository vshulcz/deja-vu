package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The state where memory quietly stops growing: the index is behind the stores
// and the directory a rebuild writes into is read-only, so `deja index` fails
// the same way every time. Three surfaces have to agree — doctor's JSON state,
// its freshness line, and the session start, which serves what it has and says
// why it is not newer. Only the rendering was covered; the state itself was
// built by hand in a report struct rather than on disk.
func TestAStaleIndexNobodyCanRebuildIsNamedEverywhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(tmp, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	write := func(id, text string, hoursAgo int) {
		t.Helper()
		d := filepath.Join(root, "-work-app")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app",`+
			`"message":{"role":"user","content":%q}}`, id, at, text)
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("m0", "the retry budget on this project", 5)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A session arrives, and then nowhere to put the rebuild.
	write("m9", "a brand new topic nobody has indexed", 1)
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o755) })

	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Index struct {
			State string `json:"state"`
		} `json:"index"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	if report.Index.State != "stale-readonly" {
		t.Errorf("index state = %q, want stale-readonly", report.Index.State)
	}

	human, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "cannot be written") {
		t.Errorf("the report does not say the index cannot be written:\n%s", doctorSection(human, "Index:"))
	}

	// And the session start serves what it has rather than going quiet.
	out := hookContextFor(t, dir, `{"hook_event_name":"SessionStart","cwd":"/work/app","session_id":"a1"}`)
	if !strings.Contains(out, "retry budget") {
		t.Errorf("the hook served nothing from an index it can still read:\n%s", out)
	}
	if !strings.Contains(out, "not writable") {
		t.Errorf("the hook does not say why the index is not newer:\n%s", out)
	}
}
