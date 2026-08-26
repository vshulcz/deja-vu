package index

import (
	"os"
	"path/filepath"
	"testing"
)

// FindByID has no harness to go on — the compact hook's payload does not carry
// one — so when two stores hold the same id it used to answer with whichever
// entry the manifest map handed over first: 340 claude against 60 codex over
// 400 identical calls (#1997). A session asking for its own evidence after a
// compaction got someone else's, and not even the same someone twice.
func TestFindByIDAnswersTheSameWayTwice(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-tmp-app")
	codex := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	for _, d := range []string{claude, codex} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))

	// The same id in two stores. The claude copy is the newer one.
	if err := os.WriteFile(filepath.Join(claude, "shared.jsonl"), []byte(
		`{"type":"user","sessionId":"shared","cwd":"/tmp/app","timestamp":"2026-03-02T03:04:05Z","message":{"role":"user","content":"the claude copy of pgbouncer"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "rollout-2026-01-01T12-00-00-shared.jsonl"), []byte(
		`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"shared","cwd":"/tmp/app"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"the codex copy of pgbouncer"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: both copies are in the index under their own identity. If
	// one of them failed to index, every answer below agrees for the wrong
	// reason.
	for _, h := range []string{"claude", "codex"} {
		if _, ok, err := FindByIdentity(dir, h, "shared"); err != nil || !ok {
			t.Fatalf("the %s copy is not in the index (%v %v), so the id is not shared", h, ok, err)
		}
	}

	// Enough calls that map order would have shown itself: the observed split
	// was 15% on the losing side, so 400 identical answers by chance is not a
	// number worth worrying about.
	first, ok, err := FindByID(dir, "shared")
	if err != nil || !ok {
		t.Fatalf("the id is in the index but FindByID missed it: %v %v", ok, err)
	}
	for i := 0; i < 400; i++ {
		got, ok, err := FindByID(dir, "shared")
		if err != nil || !ok {
			t.Fatalf("lookup %d: %v %v", i, ok, err)
		}
		if got.Harness != first.Harness {
			t.Fatalf("lookup %d answered %s after %s: the answer depends on map order",
				i, got.Harness, first.Harness)
		}
	}
	// Which one it settles on is a choice, not an accident: the freshest
	// session is the one a compacting agent is most likely to be.
	if first.Harness != "claude" {
		t.Errorf("with two copies of an id, the newer one is the better guess; got %s", first.Harness)
	}
}
