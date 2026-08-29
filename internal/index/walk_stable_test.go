package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// straddleStore writes sessions whose commands share no vocabulary between
// calls, so a rebuild interns a different table.
func straddleStore(t *testing.T, home string) (dir string, write func(prefix, cmd string, n int)) {
	t.Helper()
	root := filepath.Join(home, "claude")
	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	write = func(prefix, cmd string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s%d", prefix, i)
			p := filepath.Join(root, "proj", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-07-01T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
				fmt.Sprintf(cmd, i) + `"}}]}}` + "\n"
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return filepath.Join(home, "index.db"), write
}

// A reader takes the manifest, then opens records.bin by path. swap_window.go
// absorbs the ENOENT a swap produces; nothing covered the straddle — a manifest
// from one generation against records from the next resolves only the sessions
// both hold, and answered a third of the store with no error and nothing to
// tell the caller (#2627).
func TestARecordWalkSaysWhenItStraddledARebuild(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The rebuild lands between the manifest read and the record walk, which
	// is the window itself rather than a race against it.
	done := false
	betweenManifestAndRecords = func() {
		if done {
			return
		}
		done = true
		write("t", "cargo build --features gamma%d", 40)
		if err := Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenManifestAndRecords = nil }()

	n := 0
	err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ })
	if err == nil {
		t.Fatalf("a walk across two generations reported success with %d of 60 records", n)
	}
	if !strings.Contains(err.Error(), "rebuilt") {
		t.Fatalf("the error does not say what happened: %v", err)
	}
}

// A store nobody is rebuilding answers as it always did.
func TestARecordWalkOfAQuietStoreIsUnchanged(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ }); err != nil {
		t.Fatalf("a quiet store answered with an error: %v", err)
	}
	if n != 20 {
		t.Fatalf("the store holds 20 command records, the walk found %d", n)
	}
}

// An append is not a straddle. appendIncremental continues the interning table
// it found and carries the manifest forward, generation included, so the
// manifest read before it stays true of the log. Erroring here would make every
// hook that reads while a session is still being written give up for nothing.
func TestARecordWalkAcceptsAnAppendUnderIt(t *testing.T) {
	home := t.TempDir()
	dir, write := straddleStore(t, home)
	write("s", "go test ./alpha%d -run Beta%d", 20)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A session that grew, not a new one: a new file is a full build, and the
	// incremental path is the one a live session takes.
	grow := filepath.Join(home, "claude", "proj", "s0.jsonl")
	done := false
	betweenManifestAndRecords = func() {
		if done {
			return
		}
		done = true
		f, err := os.OpenFile(grow, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		line := `{"type":"assistant","sessionId":"s0","cwd":"/w/p","timestamp":"2026-07-01T10:05:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./alpha0 -run Beta7"}}]}}` + "\n"
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if err := Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { betweenManifestAndRecords = nil }()

	before, _ := readManifest(dir)
	n := 0
	err := EachRecordOfRole(dir, "command", func(SessionMeta, Record) { n++ })
	after, _ := readManifest(dir)
	if before.Generation != after.Generation {
		t.Skip("this store rebuilt rather than appended; the append path is what this pins")
	}
	if err != nil {
		t.Fatalf("an append under the walk was reported as a rebuild: %v", err)
	}
	if n < 20 {
		t.Fatalf("the walk lost records it had before the append: %d of at least 20", n)
	}
}
