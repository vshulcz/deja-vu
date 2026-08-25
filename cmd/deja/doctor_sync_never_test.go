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

// A machine named once and never reached is a row with neither timestamp. Three
// surfaces depend on reading it that way and none of them said so (#1859): the
// text line, the JSON row, and a consumer deciding whether an absent key means
// "never" or "a deja too old to report it".
func TestAPeerNeverReachedIsRecognisableOnBothSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	body := `{"peers":[
	{"host":"named-never-reached"},
	{"host":"desk","last_push":"2026-08-24T10:00:00Z"},
	{"host":"receives-only","last_pull":"2026-08-24T10:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if !strings.Contains(text.String(), "never exchanged") {
		t.Errorf("the text does not say a machine has never been reached:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []map[string]any `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, r := range report.Sync.Peers {
		host, _ := r["host"].(string)
		rows[host] = r
	}
	if len(rows) != 3 {
		t.Fatalf("want three peers, got %d: %#v", len(rows), report.Sync.Peers)
	}

	never := rows["named-never-reached"]
	if _, ok := never["last_push"]; ok {
		t.Errorf("a machine never reached carries a push: %#v", never)
	}
	if _, ok := never["last_pull"]; ok {
		t.Errorf("a machine never reached carries a pull: %#v", never)
	}
	// The row is still there, which is what separates "never exchanged" from
	// "a deja too old to report peers at all" — that one has no sync key.
	if _, ok := never["sessions_from_there"]; !ok {
		t.Errorf("the row is missing the key that proves it is a row: %#v", never)
	}

	// The two directions stay independent: a machine that only receives keeps
	// its pull and reports no push, which one "never" flag would flatten.
	only := rows["receives-only"]
	if _, ok := only["last_pull"]; !ok {
		t.Errorf("a machine that only receives lost its pull: %#v", only)
	}
	if _, ok := only["last_push"]; ok {
		t.Errorf("a machine that never sent carries a push: %#v", only)
	}
}
