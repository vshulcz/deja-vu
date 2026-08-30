package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config an older deja wrote can name a hook this build no longer has, and
// install leaves that line where it is — so it runs every session. It fell
// through to search, which builds the index if it has to and then reports a
// miss about "hook-session": a session start doing a full index build and
// answering nothing, once per session (#2718).
func TestAnUnknownHookNameIsNotAQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	// A harness calling the hook, not a person typing it: `go test` may or may
	// not have a terminal on stdin, and which branch this takes must not
	// depend on how the suite was started.
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return false }

	var out string
	said := captureStderr(t, func() {
		var err error
		out, err = captureRun(t, "hook-session")
		if err != nil {
			t.Errorf("a hook name a harness still calls must not fail the session: %v", err)
		}
	})
	// Nothing on stdout: a harness reads that as the hook's answer, and on
	// some of them a bare line there lands in the model's context.
	if strings.TrimSpace(out) != "" {
		t.Errorf("a hook that does not exist answered the harness:\n%s", out)
	}
	if !strings.Contains(said, "hook-session") || !strings.Contains(said, "safe to delete") {
		t.Errorf("nothing said what that line in the config is, or how to fix it:\n%s", said)
	}
	if strings.Contains(said, "no matches") || strings.Contains(said, "indexing sessions") {
		t.Errorf("the hook name was read as a query:\n%s", said)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.gob")); err == nil {
		t.Errorf("the index was built by a hook that does not exist")
	}

	// A real mistyped command is still read as a search — the reading #674
	// chose, and the right one for a word somebody typed. It goes to stderr,
	// so what says it ran is the store it built to answer.
	if _, err := captureRun(t, "sarch"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.gob")); err != nil {
		t.Errorf("a mistyped command stopped being read as a search: %v", err)
	}
}

// At a terminal the same word is somebody's typo, not a config: they keep the
// search and the near miss every other mistyped word gets (#674), and telling
// them a config calls it would be a claim about their machine that is false.
func TestATypedHookNameIsStillASearch(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"the zonkoshard retry budget"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return true }

	said := captureStderr(t, func() {
		if _, err := captureRun(t, "hook-contxt"); err != nil {
			t.Error(err)
		}
	})
	if strings.Contains(said, "safe to delete") {
		t.Errorf("a typo was answered with a claim about the reader's config:\n%s", said)
	}
	if _, err := os.Stat(filepath.Join(tmp, "index.db", "manifest.gob")); err != nil {
		t.Errorf("the word somebody typed was not searched for: %v", err)
	}
}

// And a query whose first word happens to be one: no harness passes a retired
// hook extra arguments.
func TestAQueryStartingWithAHookNameIsStillAQuery(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","message":{"role":"user","content":"hook-session timeout in codex"},` +
		`"timestamp":"2026-08-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = restore })
	stdinIsTerminal = func() bool { return false }
	out, err := captureRun(t, "hook-session", "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hook-session timeout in codex") {
		t.Errorf("a query was swallowed by the guard:\n%s", out)
	}
	_ = tmp
}
