package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rewrite that changes no byte count — `accepted` for `rejected` is the same
// eight characters — was invisible to a memo keyed on size and modification
// time, on a filesystem whose times are coarse. The windows leg hit it on every
// run; a promotion made in one MCP call was then answered stale in the next.
func TestARewriteOfTheSameLengthIsSeen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	line := func(state string) string {
		return `{"kind":"promoted","session":"claude:a","project":"app","state":"` + state +
			`","title":"t","text":"keep the pool","ts":"` + now + `"}` + "\n"
	}
	if err := os.WriteFile(path, []byte(line("accepted")), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "accepted" {
		t.Fatalf("first read: %+v", got)
	}

	// Same length and, once the stamp is put back, the same modification time —
	// which is what a coarse clock hands the memo on its own.
	if err := os.WriteFile(path, []byte(line("rejected")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := LoadPromotedNotes(); len(got) != 1 || got[0].State != "rejected" {
		t.Errorf("the rewrite was answered from the memo: %+v", got)
	}
	if got := PromotedLifecycles(); got["claude:a"].State != "rejected" {
		t.Errorf("the lifecycle came from the memo: %+v", got)
	}
}
