package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja ctx <id>` is the command the hook names to an agent, and an id is what
// every result line hands it. It printed the whole transcript of a decision
// that had been taken back, with nothing marking it — while the search screen
// and `ctx <query>` both said so (#1643).
func TestCtxByIDSaysTheDecisionWasTakenBack(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "aaaa0001-1111-4000-8000-d6e7f8a9b0c1"
	lines := []string{
		`{"type":"user","timestamp":"2026-07-10T10:00:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"user","content":"the pool keeps running out under load"}}`,
		`{"type":"assistant","timestamp":"2026-07-10T10:01:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"assistant","content":"raised the pool to 40 and it held"}}`,
	}
	if err := os.WriteFile(filepath.Join(store, "aaaa0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "promote", "aaaa0001", "--state", "rejected", "--note", "backed out, the pool was not the problem"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "ctx", "aaaa0001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rejected") {
		t.Errorf("ctx handed over a withdrawn decision with no sign of it:\n%s", out)
	}
	if !strings.Contains(out, "backed out, the pool was not the problem") {
		t.Errorf("the correction did not travel with it:\n%s", out)
	}
	// The control: a session nobody took back gets no marker.
	plain := filepath.Join(store, "bbbb0002.jsonl")
	const other = "bbbb0002-1111-4000-8000-d6e7f8a9b0c1"
	if err := os.WriteFile(plain, []byte(`{"type":"user","timestamp":"2026-07-11T10:00:00Z","sessionId":"`+other+`","cwd":"/api","message":{"role":"user","content":"the scheduler dispatches work"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "ctx", "bbbb0002")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "rejected") || strings.Contains(out, "tried and") {
		t.Errorf("a session nobody touched was marked:\n%s", out)
	}
}
