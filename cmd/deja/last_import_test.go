package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The line an import prints scrolls away; a peer who keeps the batch drops the
// same records on every sync, and doctor — where someone goes to ask why a
// peer's work never shows up — kept no record of it (#1016).
func TestDoctorRemembersWhatTheLastSyncDropped(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer record about the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// An ordinary import has nothing to report afterwards.
	var out bytes.Buffer
	doctorIndex(&out, inspectDoctorIndex(dir, nil), dir)
	if strings.Contains(out.String(), "last sync") {
		t.Errorf("an import that dropped nothing left a note behind:\n%s", out.String())
	}

	// Forget what arrived, then take the same batch again.
	id := index.ImportedSessionID("claude", "peer")
	if _, err := captureRun(t, "forget", "--session", "claude:"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorIndex(&out, inspectDoctorIndex(dir, nil), dir)
	got := out.String()
	if !strings.Contains(got, "last sync") {
		t.Errorf("doctor kept no record of what the sync dropped:\n%s", got)
	}
	if !strings.Contains(got, "left out as forgotten here") {
		t.Errorf("the line does not say why the records were dropped:\n%s", got)
	}
}
