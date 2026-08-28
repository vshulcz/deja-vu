package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A machine that has imported a peer's work and then exports its own must send
// only its own: what arrived from elsewhere belongs to the machine it came
// from, and a second hop would spread it past whoever agreed to the first.
// The roundtrip test holds this for an index that carries nothing else
// (TestSyncRoundtripAllHarnesses); this is the case a filter would actually
// break — local work and imported work side by side (#2450).
func TestAnExportCarriesOwnWorkAndNotAPeersToo(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index.db")
	store := filepath.Join(root, "claude", "-work-ownwork")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	setHome(t, root)
	at := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	own := `{"type":"user","sessionId":"own1","timestamp":"` + at + `","cwd":"/work/ownwork",` +
		`"message":{"role":"user","content":"the retry budget on main"}}`
	if err := os.WriteFile(filepath.Join(store, "own1.jsonl"), []byte(own+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := filepath.Join(root, "peer-batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peer1","project":"work/peerwork","role":"user",` +
		`"text":"the client ledger cutover on the other machine","time":"` + at + `","origin":"bravo"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(peer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, batch); err != nil || n == 0 {
		t.Fatalf("import n=%d err=%v", n, err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(root, "outgoing")
	n, err := ExportFull(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("the machine's own work did not go out at all")
	}
	files, err := filepath.Glob(filepath.Join(out, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Project string `json:"project"`
				Text    string `json:"text"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("a batch line does not decode: %v", err)
			}
			if strings.Contains(rec.Project, "peerwork") || strings.Contains(rec.Text, "other machine") {
				t.Errorf("a peer's session went out in this machine's export: %s", rec.Project)
			}
		}
	}
}
