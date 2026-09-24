package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// seedEvidenceCorpus writes sessions that both talk about a subject and run a
// command on it, so the best hit carries the evidence lines as well as the
// excerpts and the conclusions block — the fullest a hit ever gets.
func seedEvidenceCorpus(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for s := range 6 {
		sid := fmt.Sprintf("es%c", 'a'+s)
		var b strings.Builder
		for i := range 24 {
			text := strings.Repeat("flimsyshard fixture path discussion ", 8)
			if i%7 == 6 {
				text = "so the suite reads its fixture directory from FLIMSYSHARD_FIXTURES, " +
					strings.Repeat("and the default in the code points at a tree nobody ships ", 5)
			}
			fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":%q},"timestamp":"2026-08-0%dT1%d:00:00Z","sessionId":"%s","cwd":"/proj"}`+"\n",
				text, s+1, i%10, sid)
		}
		// The command, and the result that says it passed.
		fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd /proj && FLIMSYSHARD_FIXTURES=./fixtures make test"}}]},"timestamp":"2026-08-0%dT19:00:00Z","sessionId":"%s","cwd":"/proj"}`+"\n", s+1, sid)
		fmt.Fprintf(&b, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok\tproj/store\t0.6s"}]},"timestamp":"2026-08-0%dT19:00:01Z","sessionId":"%s","cwd":"/proj"}`+"\n", s+1, sid)
		if err := os.WriteFile(filepath.Join(store, sid+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The evidence lines are appended after the page has been measured, the same
// way the conclusions block is, and what bounds the reply is the outer trim.
// Pinned across the budgets the conclusions gate was measured at (#1319): the
// page an agent receives is never longer than the budget it was given, with the
// lines present.
func TestTheEvidenceLinesNeverPushThePageOverItsBudget(t *testing.T) {
	dir := seedEvidenceCorpus(t)
	full, _, _, _, err := recallTextResult(dir, "flimsyshard fixture path", "", 0, 0, 4096-recallFrameOverhead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "it ran:") {
		t.Fatalf("the corpus produced no evidence line, so this proves nothing:\n%s", full)
	}
	if !strings.Contains(full, "exit 0 here") {
		t.Errorf("the command the transcript saw pass does not say so:\n%s", full)
	}
	for _, budget := range []int{4096 - recallFrameOverhead, 3000, 2400, 1200, 600} {
		text, _, _, _, err := recallTextResult(dir, "flimsyshard fixture path", "", 0, 0, budget)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if len(text) > budget {
			t.Errorf("budget %d produced %d bytes", budget, len(text))
		}
	}
}
