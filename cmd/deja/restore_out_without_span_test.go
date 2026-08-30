package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `-o` says where the bytes should go. Without `--span` it was accepted and
// ignored: the listing printed, nothing was written, and nothing said so —
// the state #2253 closed one step over (#2417).
func TestRestoreSaysWhatItDidWithTheFileItWasGiven(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	edit := func(sid, old string, ago time.Duration) string {
		at := time.Now().Add(-ago).UTC().Format(time.RFC3339)
		return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + at + `","cwd":"/work/app",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":` +
			`{"file_path":"/work/app/retry.go","old_string":"` + old + `","new_string":"// replaced\n"}}]}}`
	}
	dir := index.DefaultDir()

	t.Run("one span goes where it was asked to go", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(store, "one.jsonl"),
			[]byte(edit("one", "func Budget() int { return 3 }", time.Hour)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmp, "recovered.go")
		var buf bytes.Buffer
		if err := runRestore(dir, []string{"retry.go", "-o", out}, &buf); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("the file deja was handed was never written: %v\n%s", err, buf.String())
		}
		if !strings.Contains(string(body), "return 3") {
			t.Errorf("the span did not land in the file:\n%s", body)
		}
	})

	t.Run("several spans say which one to name", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(store, "two.jsonl"),
			[]byte(edit("two", "func Budget() int { return 5 }", 30*time.Minute)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmp, "ambiguous.go")
		var buf bytes.Buffer
		if err := runRestore(dir, []string{"retry.go", "-o", out}, &buf); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("deja picked a span on its own")
		}
		if !strings.Contains(buf.String(), "--span") || !strings.Contains(buf.String(), out) {
			t.Errorf("nothing said the file was not written:\n%s", buf.String())
		}
	})
}
