package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ctx reads an argument as an id only from six characters up, so a shorter
// prefix fell through to the text search and came back "no session matches" —
// about a session `deja show` opens on the same prefix (#1614).
func TestCtxOpensAShortIdPrefix(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, ts string) string {
		return `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"` + ts + `","sessionId":"` + sid + `","cwd":"/proj"}` + "\n"
	}
	for _, s := range []struct{ id, ts string }{
		{"ffff3333-1111-4000-8000-d6e7f8a9b0c1", "2026-07-01T10:00:00Z"},
		{"abc12aaa-2222-4000-8000-d6e7f8a9b0c1", "2026-07-02T10:00:00Z"},
	} {
		if err := os.WriteFile(filepath.Join(store, s.id+".jsonl"), []byte(line(s.id, s.ts)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "ctx", "ffff")
	if err != nil {
		t.Fatalf("ctx ffff: %v", err)
	}
	if !strings.Contains(out, "ffff3333-1111-4000-8000-d6e7f8a9b0c1") {
		t.Errorf("ctx did not open the session the prefix names:\n%s", out)
	}
	// The control: a short word that names no session is still a miss, and
	// still says so.
	if _, err := captureRun(t, "ctx", "zqx"); err == nil {
		t.Error("ctx zqx was answered, so the id fallback swallowed a real miss")
	}
}
