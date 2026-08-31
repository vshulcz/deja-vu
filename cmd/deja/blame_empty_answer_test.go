package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// blame is the tool an agent calls before it edits a file, and on a machine
// with no history it answered a bare `[]` — no note, nothing. The empty array
// is the whole answer, so an agent has no way to tell "nobody ever touched this
// file" from "deja has nothing indexed at all", which is the distinction #2862
// drew for recall.
func TestBlameSaysWhyItFoundNothing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"main.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "no indexed history") {
		t.Errorf("an empty store answers with nothing at all:\n%s", text)
	}
	// Still JSON, still an array: an agent parses this.
	var out []map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("the answer stopped being a JSON array (%v):\n%s", err, text)
	}
}

// A store that holds sessions, asked about a file none of them touched, keeps
// its own answer: nothing was indexed is not true there.
func TestBlameOnAStoreWithHistoryDoesNotCallItEmpty(t *testing.T) {
	dir := manySessionStore(t, 3)
	text, err := callMCPTool(dir, "blame", json.RawMessage(`{"path":"nowhere-near-this-store.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "no indexed history") {
		t.Errorf("a store with sessions in it was called empty:\n%s", text)
	}
}
