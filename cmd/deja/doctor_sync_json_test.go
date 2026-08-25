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

// A script reads this to act on it — `deja sync ssh <host>` — so the name goes
// out as written. The encoder escapes a control byte by itself, which is why
// the text report needs a bound here and the JSON does not (#1838).
func TestTheJSONReportNamesThePeerExactly(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	long := strings.Repeat("w", 300) + ".example.net"
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": long, "last_push": "2026-08-22T10:00:00Z"},
		{"host": "red\x1b[31malert", "last_push": "2026-08-22T10:00:00Z"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peersFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	// The bytes on the wire carry no raw escape: the encoder wrote .
	if strings.ContainsAny(out.String(), "\x1b\r") {
		t.Errorf("the encoded report carries a raw escape or rewind")
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host string `json:"host"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	var hosts []string
	for _, p := range report.Sync.Peers {
		hosts = append(hosts, p.Host)
	}
	if len(hosts) != 2 {
		t.Fatalf("want two peers, got %d", len(hosts))
	}
	found := map[string]bool{hosts[0]: true, hosts[1]: true}
	if !found[long] {
		t.Errorf("the long host came back changed, so a script cannot address it: %q", hosts)
	}
	if !found["red\x1b[31malert"] {
		t.Errorf("the host with an escape byte came back changed: %q", hosts)
	}
}

// sessions_from_there is the number that says whether anything ever actually
// arrived from a machine, so it has to be the real count rather than a key.
func TestTheJSONReportCountsWhatArrivedFromEachPeer(t *testing.T) {
	tmp := hermeticEnv(t)
	// A name wider than the column the text report bounds to, so that keying
	// the count on the displayed name instead of the stored one shows up as a
	// zero rather than passing by luck.
	const machine = "mini-with-a-name-longer-than-the-eighty-columns-the-text-report-bounds-a-host-to.example.net"
	t.Setenv("DEJA_MACHINE", machine)
	src := filepath.Join(tmp, "src.db")
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"fromMini","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the pool keeps running dry on staging"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(src, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if _, err := index.ExportFull(src, batch); err != nil {
		t.Fatal(err)
	}

	// A different machine reads that batch in.
	t.Setenv("DEJA_MACHINE", "laptop")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	dst := index.DefaultDir()
	if _, err := index.Import(dst, batch); err != nil {
		t.Fatal(err)
	}
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	peersBody, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": machine, "last_pull": "2026-08-22T10:00:00Z"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peersFile, peersBody, 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dst); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host     string `json:"host"`
				Sessions int    `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	if got := report.Sync.Peers[0].Sessions; got != 1 {
		t.Errorf("sessions_from_there = %d, want the one session that arrived from that machine", got)
	}
}
