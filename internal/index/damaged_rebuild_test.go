package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A half-written index rebuilds itself, and it used to announce that with the
// same line as a routine reindex — so a disk that keeps corrupting the store
// looked ordinary every time (#1110).
func TestDamagedIndexSaysSoWhileRebuilding(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"a decision about backpressure"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	var first bytes.Buffer
	if err := EnsureForSearch(dir, query.Options{}, false, &first); err != nil {
		t.Fatal(err)
	}
	half := func(name string) {
		p := filepath.Join(dir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b[:len(b)/2], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"manifest.gob", "records.bin"} {
		half(name)
		var out bytes.Buffer
		if err := EnsureForSearch(dir, query.Options{}, false, &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(out.String(), "could not be read and is being rebuilt") {
			t.Errorf("a half-written %s rebuilt silently: %q", name, out.String())
		}
	}

	// A store that is merely stale must not borrow that wording.
	var routine bytes.Buffer
	if err := EnsureForSearch(dir, query.Options{}, false, &routine); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(routine.String(), "could not be read") {
		t.Errorf("an intact index was called damaged: %q", routine.String())
	}
}
