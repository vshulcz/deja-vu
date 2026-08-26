package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// The question every append-only writer here asks before it writes: did the
// last one finish? Three of them got it wrong in turn — the usage log (#1901),
// the injection snapshots (#1965) and the hook dedup file (#1967) — so it lives
// in one place now.
func TestEndsMidLine(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name string
		body string
		want bool
	}{
		{"a file that does not exist yet", "", false},
		{"a whole line", "one\n", false},
		{"a line cut before its newline", "one", true},
		{"whole lines then half of one", "one\ntwo\nthr", true},
		{"a trailing blank line", "one\n\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(dir, c.name)
			if c.body != "" {
				if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if got := EndsMidLine(f); got != c.want {
				t.Errorf("EndsMidLine(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

// A descriptor opened write-only cannot answer the question, and says the file
// ends cleanly rather than failing — the callers are bookkeeping, and none of
// them may break a recall. Worth pinning because it is the trap: the fix for
// all three files was as much the open mode as the check.
func TestEndsMidLineOnAWriteOnlyDescriptorSaysClean(t *testing.T) {
	p := filepath.Join(t.TempDir(), "half")
	if err := os.WriteFile(p, []byte("cut"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if EndsMidLine(f) {
		t.Error("a write-only descriptor reported a truncated tail it cannot read")
	}
}
