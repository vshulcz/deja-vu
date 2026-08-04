package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An index emptied by the reader's own forget is not an unbuilt one, and "run
// `deja index`" cannot bring a tombstoned session back. search has said so
// since #844; the two listing screens read it as data loss (#1007).
func TestListingsSayWhenTheStoreWasEmptiedByAForget(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"the ticker window stays at 30s"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "forget", "--session", "loc"); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"last", "stats"} {
		out, err := captureRunStderr(t, cmd)
		if err != nil {
			t.Fatal(err)
		}
		combined := out
		if cmd == "stats" {
			text, err := captureRun(t, cmd)
			if err != nil {
				t.Fatal(err)
			}
			combined += text
		}
		if !strings.Contains(combined, "forgotten") {
			t.Errorf("%s does not say the store was emptied by a forget:\n%s", cmd, combined)
		}
		if strings.Contains(combined, "run `deja index`") {
			t.Errorf("%s advises a build that cannot bring it back:\n%s", cmd, combined)
		}
	}
}
