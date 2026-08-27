package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// countingProgress records what the reading stage promised and what it
// delivered.
type countingProgress struct {
	mu       sync.Mutex
	phase    string
	total    map[string]int
	advanced map[string]int
}

func (c *countingProgress) Phase(name string, total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.phase = name
	c.total[name] = total
}

func (c *countingProgress) Advance(units int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanced[c.phase] += units
}

func (c *countingProgress) Harness(string, int, int) {}

// The bar is sized in files and moved in stores. It counted a file under its
// kind — "cline-sdk" — and advanced by the store the parse loop names, so a
// corpus of cline sessions filled the total and left the bar where it was
// (#2242).
func TestEveryFileCountedInTheBarAlsoMovesIt(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", cline)

	stamp := time.Now().Add(-time.Hour).UnixMilli()
	for i := 0; i < 4; i++ {
		body := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user","content":"hello there"}]}`, stamp)
		name := fmt.Sprintf("s%d.messages.json", i)
		if err := os.WriteFile(filepath.Join(cline, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"hello there"}}`, at)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &countingProgress{total: map[string]int{}, advanced: map[string]int{}}
	SetProgress(p)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	total, advanced := p.total["reading sessions"], p.advanced["reading sessions"]
	p.mu.Unlock()
	// The premise: every file on disk is in the total, or the bar being short
	// proves nothing.
	if total != 5 {
		t.Fatalf("the reading stage was sized at %d for five files on disk", total)
	}
	if advanced != total {
		t.Errorf("the reading stage promised %d files and moved the bar by %d", total, advanced)
	}
}
