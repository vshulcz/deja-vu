package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The live display erases itself when the build ends, so an interactive
// rebuild left an empty screen: an animation ran for seconds and nothing said
// what was indexed. Piped output has printed the counts all along (#867).
func TestRebuildOnATerminalSaysWhatItIndexed(t *testing.T) {
	tmp := hermeticEnv(t)
	chats := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"pool exhausted"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// hermeticEnv points the warmup sentinel at a guard path for every test in
	// this package; this case is the ordinary interactive run.
	t.Setenv("DEJA_WARMUP_SENTINEL", "")

	saved := logoWanted
	logoWanted = func(*os.File) bool { return true }
	t.Cleanup(func() { logoWanted = saved })

	run := func(t *testing.T) string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := os.Stderr
		os.Stderr = w
		err = cmdIndex(dir, []string{"--rebuild"})
		os.Stderr = stderr
		_ = w.Close()
		out, _ := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	got := run(t)
	if !strings.Contains(got, "indexed 1 session, 1 message") {
		t.Errorf("rebuild said nothing about what it built:\n%s", got)
	}

	// Not on the warmup child: nobody is watching its screen, and its output
	// goes to /dev/null.
	t.Setenv("DEJA_WARMUP_SENTINEL", filepath.Join(tmp, "sentinel"))
	if got := run(t); strings.Contains(got, "indexed 1 session") {
		t.Errorf("the detached warmup narrated to nobody:\n%s", got)
	}
}
