package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The carry-forward drops the old counts of a file the pass is re-reading, so
// the fresh read is the whole story. It decided that by file kind while the key
// became the store in #2239, so for five stores nothing matched and every
// incremental pass added the file's secrets on top of the ones already counted
// (#2240).
func TestAReReadFileDoesNotCountItsRedactionsTwice(t *testing.T) {
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

	secret := "ghp_" + strings.Repeat("a", 36)
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	path := filepath.Join(cline, "one.messages.json")
	write := func(turns int) {
		t.Helper()
		var b strings.Builder
		b.WriteString(`{"agent":"lead","messages":[`)
		for i := 0; i < turns; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"ts":%d,"role":"user","content":"the token is %s"}`, stamp+int64(i), secret)
		}
		b.WriteString("]}")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	counted := func() int {
		t.Helper()
		stats, err := Redactions(filepath.Join(tmp, "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		return stats.Rules["cline"]["github-token"]
	}

	dir := filepath.Join(tmp, "index.db")
	write(1)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: one secret in the file, one on the counter.
	if got := counted(); got != 1 {
		t.Fatalf("the first build counted %d of one secret, so this measures nothing", got)
	}

	write(2)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := counted(); got != 2 {
		t.Errorf("two secrets in the file, %d counted after an incremental pass", got)
	}

	// And again, to catch a carry that only shows on the second repeat.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := counted(); got != 2 {
		t.Errorf("a pass with nothing new to read moved the count to %d", got)
	}
}
