package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "This machine has no indexed history yet" is the right thing to say on a
// first run (#2862, #2863, #2865) and the wrong thing to say after the user
// forgot everything: the history existed, and "yet" claims it never did.
//
// deja knows the difference — forgetting leaves tombstones — and the CLI
// already says so on its own screen: "this machine also has 2 sessions
// forgotten (`deja forget --list`)".
func TestAnEmptiedStoreIsNotCalledAFirstRun(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"the zonkomatic deploy"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "s1"); err != nil {
		t.Fatal(err)
	}
	metas, err := index.AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("the fixture still holds %d sessions, so this is not the emptied case", len(metas))
	}

	for _, tc := range []struct{ tool, args string }{
		{"recall", `{"query":"zonkomatic"}`},
		{"blame", `{"path":"main.go"}`},
		{"how", `{"what":"zonkomatic"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "no indexed history yet") {
			t.Errorf("%s: an emptied store is reported as a first run:\n%s", tc.tool, text)
		}
		if !strings.Contains(text, "forgotten") {
			t.Errorf("%s: nothing says the history was removed rather than absent:\n%s", tc.tool, text)
		}
		// The count, not just the word: "some sessions have been forgotten"
		// leaves the reader where they started, and a sentence that cannot
		// say how many is a sentence that never read the tombstones.
		if !strings.Contains(text, "1 session has been forgotten") {
			t.Errorf("%s: the sentence does not say how much was removed:\n%s", tc.tool, text)
		}
	}
}
