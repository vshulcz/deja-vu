package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// Every result line deja prints carries an id, and `deja ctx <id>` opens the
// session it names. The MCP tool an agent is told to call took the same string
// as words only, so an agent holding an id deja had just printed was told the
// session does not exist (#1622).
func TestRecallContextOpensASessionByID(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "hhhh0001-1111-4000-8000-d6e7f8a9b0c1"
	line := `{"type":"user","message":{"role":"user","content":"the pool exhausted again"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"hhhh0001", id} {
		text, n, _, ids, err := recallContextResult(dir, q, "")
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 1 || len(ids) != 1 || ids[0] != id {
			t.Errorf("%s returned n=%d ids=%v; the id names one session\n%s", q, n, ids, text)
			continue
		}
		if !strings.Contains(text, id) {
			t.Errorf("%s: the context does not name the session:\n%s", q, text)
		}
	}
	// The controls: words still answer, and a string that names nothing still
	// comes back empty rather than opening something.
	if _, n, _, _, err := recallContextResult(dir, "pool exhausted", ""); err != nil || n != 1 {
		t.Errorf("words: n=%d err=%v", n, err)
	}
	if text, n, _, _, err := recallContextResult(dir, "zzzz9999", ""); err != nil || n != 0 {
		t.Errorf("a string that names nothing returned n=%d err=%v:\n%s", n, err, text)
	}
}
