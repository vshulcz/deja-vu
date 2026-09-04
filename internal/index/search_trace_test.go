package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// A reader who waits seconds for an answer can say which part of the wait it
// was: taking the lock, walking the stores, or ingesting what changed. Without
// this the three are one number and a report can only guess (#3021).
func TestSearchTraceNamesTheStages(t *testing.T) {
	root := t.TempDir()
	setHome(t, root)
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	store := filepath.Join(root, "claude", "-work-app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","cwd":"/work/app",` +
		`"message":{"role":"user","content":"tracedneedle in the loader"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// One more turn, so the search has something to ingest inline.
	more := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","cwd":"/work/app",` +
		`"message":{"role":"assistant","content":"tracedneedle answered"}}` + "\n"
	f, err := os.OpenFile(filepath.Join(store, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEJA_TRACE", "1")
	got := captureStderr(t, func() {
		if _, err := EnsureForSearchStale(dir, query.Options{Query: "tracedneedle"}, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, stage := range []string{"walk stores", "append"} {
		if !strings.Contains(got, stage) {
			t.Errorf("the trace does not name %q:\n%s", stage, got)
		}
	}

	// And it is silent otherwise: this runs on every search.
	t.Setenv("DEJA_TRACE", "")
	quiet := captureStderr(t, func() {
		if _, err := EnsureForSearchStale(dir, query.Options{Query: "tracedneedle"}, nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(quiet, "trace ") {
		t.Errorf("the trace printed without being asked:\n%s", quiet)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}
