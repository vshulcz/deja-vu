package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The second session someone ever has is the first time deja can say "you have
// been here", and it said nothing: in a one-session store every idf collapses
// to zero, so the term the session had to have SPOKEN was picked by the prompt's
// word order and came out "seeing" (#2257).
func TestAStoreOfOneSessionStillRecallsIt(t *testing.T) {
	inject := func(t *testing.T, fillers int) string {
		t.Helper()
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		claude := filepath.Join(tmp, "claude")
		if err := os.MkdirAll(filepath.Join(claude, "beta"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(claude, "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_CLAUDE_ROOT", claude)
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
		t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
		at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":"hookfix","timestamp":%q,`+
			`"message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`, at)
		if err := os.WriteFile(filepath.Join(claude, "beta", "hookfix.jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < fillers; i++ {
			l := fmt.Sprintf(`{"type":"user","sessionId":"filler%d","timestamp":%q,`+
				`"message":{"role":"user","content":"unrelated work about deployments and dashboards"}}`, i, at)
			if err := os.WriteFile(filepath.Join(claude, "alpha", fmt.Sprintf("f%d.jsonl", i)), []byte(l+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(tmp, "proj", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"seeing gateway_timeout in the reconnect_loop again"}`)
		if err := runHookPrompt(dir, in, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// The premise: with a second session in the store this prompt recalls.
	if got := inject(t, 1); !strings.Contains(got, "gateway_timeout") {
		t.Fatalf("two sessions and no recall, so this measures nothing: %q", got)
	}
	if got := inject(t, 0); !strings.Contains(got, "gateway_timeout") {
		t.Errorf("the only session in the store was not recalled: %q", got)
	}
}
