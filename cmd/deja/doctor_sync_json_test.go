package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A sync that stops does not announce itself, which is why the text report has
// a Sync section — and `--json`, the one reader that could watch it unattended,
// had no key for it at all (#1838). Same gap the policy block had in #1027.
func TestTheJSONReportCarriesTheSyncState(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	body := `{"peers":[
	{"host":"laptop","last_push":"2026-08-22T10:00:00Z","last_pull":"2026-08-22T10:00:00Z"},
	{"host":"build-box","last_push":"2026-08-20T10:00:00Z","last_error":"ssh build-box: exit status 255"}]}`
	if err := os.WriteFile(peersFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out.String())
	}
	sync, ok := report["sync"].(map[string]any)
	if !ok {
		t.Fatalf("no sync key in the report; keys are %v", reportKeys(report))
	}
	rows, ok := sync["peers"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("want two peers under sync, got %#v", sync["peers"])
	}
	first, _ := rows[0].(map[string]any)
	if first["host"] != "build-box" && first["host"] != "laptop" {
		t.Errorf("a peer row does not name its machine: %#v", first)
	}
	// The thing the section exists for: a peer that keeps failing.
	var failing map[string]any
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["host"] == "build-box" {
			failing = row
		}
	}
	if failing == nil {
		t.Fatalf("the failing peer is missing: %#v", rows)
	}
	if failing["last_error"] == nil || !strings.Contains(failing["last_error"].(string), "255") {
		t.Errorf("the failure a script would watch for is not in the row: %#v", failing)
	}
	if failing["last_pull"] != nil {
		t.Errorf("a machine that has never sent anything claims a pull: %#v", failing)
	}
	if _, ok := failing["last_push"].(string); !ok {
		t.Errorf("the row does not say when this machine last pushed: %#v", failing)
	}
	if _, ok := failing["sessions_from_there"]; !ok {
		t.Errorf("the row does not say how much of this index came from there: %#v", failing)
	}
}

// A machine with no peers still gets the key, so a script can tell "no machines
// configured" from "this deja is too old to say".
func TestTheJSONReportSaysWhenThereAreNoPeers(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "peers.json"))
	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	sync, ok := report["sync"].(map[string]any)
	if !ok {
		t.Fatalf("no sync key on a machine with no peers; keys are %v", reportKeys(report))
	}
	if rows, ok := sync["peers"].([]any); ok && len(rows) != 0 {
		t.Errorf("peers were invented: %#v", rows)
	}
}

// reportKeys names what the report carried, for a failure message.
func reportKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
