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

// "no matches in 1 indexed session — try fewer words" is advice about wording
// for a session the rule never let search open: the count named the whole
// store rather than the part this path may read (#986).
func TestNoMatchesCountsWhatSearchMayRead(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer work on the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// Without a rule the miss is a wording problem and reads like one.
	var out bytes.Buffer
	printNoMatches(&out, dir, "ticker window", false)
	if !strings.Contains(out.String(), "no matches in 1 indexed session") {
		t.Errorf("an ordinary miss lost its count:\n%s", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	printNoMatches(&out, dir, "ticker window", false)
	got := out.String()
	if strings.Contains(got, "try fewer words") {
		t.Errorf("the reader was sent after their wording:\n%s", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("nothing named the rule that emptied the search path:\n%s", got)
	}
	if strings.Contains(got, "in 1 indexed session") {
		t.Errorf("the count still names sessions search cannot read:\n%s", got)
	}
}
