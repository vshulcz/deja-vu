package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A session that opens with a compaction summary is titled by the person's
// first real line, not by "Summary: 1. Primary Request and Intent: …" — the
// line the status bar quoted on 2026-09-08 (#3157).
func TestOpenerSkipsTheCompactionSummaryAsATitle(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "compact.jsonl"), "compact", []string{
		`{"type":"user","sessionId":"compact","timestamp":"` + old + `","message":{"role":"user","content":"Summary:\n1. Primary Request and Intent:\n   - MOST RECENT: fix the exporter_batch rows dropped at utc_midnight\n2. Key Technical Concepts:\n   - exporter_batch cursor"}}`,
		`{"type":"assistant","sessionId":"compact","timestamp":"` + old + `","message":{"role":"assistant","content":"Continuing from the summary."}}`,
		`{"type":"user","sessionId":"compact","timestamp":"` + old + `","message":{"role":"user","content":"the exporter_batch job drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"compact","timestamp":"` + old + `","message":{"role":"assistant","content":"utc_midnight rollover: the exporter_batch cursor was compared in local time."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	in := `{"prompt":"exporter_batch dropping rows at utc_midnight again","session_id":"agent-7"}`
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "Primary Request") {
		t.Fatalf("the compaction summary became a title:\n%s", got)
	}
	if !strings.Contains(got, "déjà vu: the exporter_batch job drops rows at utc_midnight") {
		t.Fatalf("the person's line was not the title:\n%s", got)
	}
}
