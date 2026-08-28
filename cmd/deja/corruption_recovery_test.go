package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A machine that lost power mid-write leaves one of the index files garbage.
// deja rebuilds from the transcripts and answers anyway — and a session that
// was forgotten stays forgotten, because the tombstones do not live in the
// index. Nothing pinned either half.
func TestADamagedIndexIsRebuiltAndKeepsItsTombstones(t *testing.T) {
	for _, broken := range []string{"manifest.gob", "sessions.gob", "records.bin"} {
		t.Run(broken, func(t *testing.T) {
			tmp := t.TempDir()
			home := filepath.Join(tmp, "home")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			claude := filepath.Join(tmp, "claude", "-work-app")
			if err := os.MkdirAll(claude, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
			t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
			t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
			t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
			t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
			at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
			for i := 1; i <= 3; i++ {
				line := fmt.Sprintf(`{"type":"user","sessionId":"sess%d","timestamp":%q,"cwd":"/work/app",`+
					`"message":{"role":"user","content":"gateway_timeout number %d"}}`, i, at, i)
				name := fmt.Sprintf("s%d.jsonl", i)
				if err := os.WriteFile(filepath.Join(claude, name), []byte(line+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			dir := filepath.Join(tmp, "index.db")
			if err := index.Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := index.Forget(dir, index.ForgetOptions{Session: "sess2"}); err != nil {
				t.Fatal(err)
			}

			// Through the command, not the library: a damaged index is an
			// error to index.Search and the CLI is what rebuilds around it.
			ids := func() []string {
				t.Helper()
				out, err := captureRun(t, "search", "gateway_timeout", "--all")
				if err != nil {
					t.Fatalf("search after %s: %v", broken, err)
				}
				var got []string
				for i := 1; i <= 3; i++ {
					id := fmt.Sprintf("sess%d", i)
					if strings.Contains(out, id) {
						got = append(got, id)
					}
				}
				return got
			}

			// The premise: two of three answer, and the forgotten one does not.
			before := strings.Join(ids(), ",")
			if before != "sess1,sess3" {
				t.Fatalf("before the damage the index answers %q", before)
			}

			if err := os.WriteFile(filepath.Join(dir, broken), []byte("garbage"), 0o600); err != nil {
				t.Fatal(err)
			}

			after := ids()
			if len(after) != 2 {
				t.Errorf("%s was damaged and the search returned %v", broken, after)
			}
			for _, id := range after {
				if id == "sess2" {
					t.Errorf("%s was damaged and the forgotten session came back", broken)
				}
			}
		})
	}
}
