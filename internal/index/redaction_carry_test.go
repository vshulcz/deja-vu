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

// And the same file, in an index whose rules were written before the fold: the
// stale kind key has to go when its file is re-read, or the report folds it
// into the fresh count and reports both (#2240).
func TestAStaleKindKeyIsDroppedWhenItsFileIsReRead(t *testing.T) {
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
	turn := func(i int) string {
		return fmt.Sprintf(`{"ts":%d,"role":"user","content":"the token is %s"}`, stamp+int64(i), secret)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := os.WriteFile(path, []byte(`{"agent":"lead","messages":[`+turn(0)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The manifest as a deja from before the fold left it.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.RedactionRules = map[string]int{"cline-sdk:github-token": 1}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"agent":"lead","messages":[`+turn(0)+`,`+turn(1)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats.Rules["cline"]["github-token"]; got != 2 {
		t.Errorf("two secrets in the file, the report says %d", got)
	}
}
