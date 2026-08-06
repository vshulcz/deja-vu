package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A message longer than maxIndexedText is stored clipped. The session stays
// searchable, so a query over the missing tail answers "no matches" — which
// reads as "you never said that". The manifest already promises the opposite:
// silent loss must be diagnosable (#1093).
func TestClippedMessageIsCounted(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	big := strings.Repeat("lorem ipsum dolor sit amet ", 4000) // ~108 KB, past the 64 KB cap
	lines := []string{
		`{"type":"user","sessionId":"s-huge","cwd":"/tmp/big","timestamp":"2026-06-01T09:00:00Z","message":{"role":"user","content":"gudgeonpin opening line"}}`,
		fmt.Sprintf(`{"type":"user","sessionId":"s-huge","cwd":"/tmp/big","timestamp":"2026-06-01T09:01:00Z","message":{"role":"user","content":"%s tailneedle"}}`, big),
	}
	writeClaudeFixture(t, filepath.Join(root, "projects", "-tmp-big", "s-huge.jsonl"), "s-huge", lines)
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the tail really is gone, so the count is the only way to know.
	out, err := captureRun(t, "search", "--json", "tailneedle")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.Total != 0 {
		t.Fatalf("the fixture did not clip anything; total = %d", res.Total)
	}

	health := index.IngestHealth(dir)
	if got := health["claude"].ClippedMessages; got != 1 {
		t.Errorf("clipped_messages = %d, want 1 — the clip left no trace", got)
	}

	doctor, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctor, "stored short of the transcript") {
		t.Errorf("doctor does not mention the clipped message:\n%s", doctor)
	}
}
