package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A decision promoted on one machine travels: it arrives as a session carrying
// its lifecycle state, recall ranks it as a decision and the tool hook honours
// it (#975). The page's Notes tab is built from this machine's notes file
// alone, so the tab that holds decisions held none of the ones that came from
// elsewhere (#2421).
func TestThePageCarriesADecisionThatArrivedBySync(t *testing.T) {
	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := `{"harness":"deja","session_id":"deja-note-claude-a1","project":"work/api","role":"user",` +
		`"text":"[accepted] retry budget stays at 5 (from claude:a1)","time":"` + at + `",` +
		`"origin":"quicksilver","lifecycle":"accepted","lifecycle_note":"retry budget stays at 5",` +
		`"lifecycle_at":"` + at + `"}`
	if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(rec+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	captureBoth(t, "sync", "import", batch)
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "page.html")
	if _, _, err := writeViewHTML(dir, out); err != nil {
		t.Fatal(err)
	}
	page := readFileString(t, out)
	m := regexp.MustCompile(`(?s)const S=(.*?),R=(.*?),N=(.*?);\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page's script no longer starts with the three arrays")
	}
	var carried []map[string]any
	if err := json.Unmarshal([]byte(m[3]), &carried); err != nil {
		t.Fatalf("the notes array does not decode: %v", err)
	}
	found := false
	for _, n := range carried {
		text, _ := n["text"].(string)
		state, _ := n["state"].(string)
		if strings.Contains(text, "retry budget stays at 5") {
			found = true
			if state != "accepted" {
				t.Errorf("the decision arrived without its state: %v", n)
			}
		}
	}
	if !found {
		t.Errorf("a decision that arrived by sync is on no tab that holds decisions:\n%s", m[3])
	}
}
