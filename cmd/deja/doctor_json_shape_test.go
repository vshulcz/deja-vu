package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/json-output.md is the contract for anyone parsing this. It names the
// three keys that may be absent — embed, ingest_health, deep — so every other
// documented field has to be there. stale_stores carried omitempty, so the
// zero the example shows was the one value never written, and a script reading
// it raised on every machine with a fresh index (#1710).
func TestDoctorJSONCarriesEveryDocumentedField(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"schema_version", "stores", "index", "mcp", "sqlite3", "version", "policy"} {
		if _, ok := report[key]; !ok {
			t.Errorf("%q is missing from the report", key)
		}
	}
	idx, _ := report["index"].(map[string]any)
	if _, ok := idx["stale_stores"]; !ok {
		t.Errorf("index.stale_stores is documented and absent: %v", idx)
	}
}

// The policy path in the docs has to be the file deja actually reads.
func TestDocsNameThePolicyFileDejaWrites(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "json-output.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "policy.toml") {
		t.Error("docs/json-output.md names policy.toml; deja reads and writes policy.json")
	}
}
