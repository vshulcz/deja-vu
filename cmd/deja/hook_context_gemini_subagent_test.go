package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Gemini 0.64 writes a sub-agent's run to chats/<parent>/<child>.jsonl and the
// reader marks it "subagent" (#5134). The digest dropped only Claude's
// "sidechain", so a failed Gemini child took the second slot of a new
// session's digest beside its own parent (#5215).
func TestTheDigestDropsAGeminiSubagentRun(t *testing.T) {
	withStatsStores(t)
	root := filepath.Join(t.TempDir(), ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	cwd := filepath.Join(t.TempDir(), "proj-q")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	idDir := filepath.Join(root, "tmp", "proj-q")
	chats := filepath.Join(idDir, "chats")
	parent := "ae2dcdfc-e6a9-4d20-a11b-168700af1f82"
	child := "9df5a191-6fe9-4a0a-8438-c1fb59406a23"
	if err := os.MkdirAll(filepath.Join(chats, parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, ".project_root"), []byte(cwd), 0o644); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	head := func(id, kind, at string) string {
		return `{"sessionId":"` + id + `","projectHash":"c2c2","startTime":"` + at + `","lastUpdated":"` + at + `","kind":"` + kind + `"}`
	}
	write := func(path string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(chats, "session-2026-10-18T05-25-ae2dcdfc.jsonl"),
		head(parent, "main", older),
		`{"id":"u1","timestamp":"`+older+`","type":"user","content":[{"text":"the wombatgate export drops rows over 4 KB"}]}`,
		`{"id":"g1","timestamp":"`+older+`","type":"gemini","content":"the fix: the wombatgate batcher caps the payload at 4 KB, raised to 64 KB"}`,
	)
	write(filepath.Join(chats, parent, child+".jsonl"),
		head(child, "subagent", recent),
		`{"id":"u2","timestamp":"`+recent+`","type":"user","content":[{"text":"Fix the wombatgate config."}]}`,
		`{"id":"g2","timestamp":"`+recent+`","type":"gemini","content":"I couldn't complete the edit because the required tool call was interrupted."}`,
	)
	t.Chdir(cwd)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := hookDigest(index.DefaultDir())
	if !strings.Contains(got, "wombatgate batcher") {
		t.Fatalf("the parent is missing, so this measures nothing:\n%s", got)
	}
	if strings.Contains(got, "required tool call was interrupted") || strings.Contains(got, child[:8]) {
		t.Errorf("the Gemini sub-agent run is quoted in the digest beside its parent:\n%s", got)
	}
}
