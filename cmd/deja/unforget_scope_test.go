package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// forget refuses a prefix that reaches more than one session; the undo restored
// them all and reported the count afterwards, while the hint beside the list
// promises that it brings one back (#961).
func TestUnforgetRefusesAPrefixThatReachesSeveral(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"ab-one", "ab-two", "zz-other"} {
		rec := `{"type":"user","message":{"role":"user","content":"session ` + id + `"},"timestamp":"2026-07-1` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--session", "ab", "--all-matches"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("ab"); n != 2 {
		t.Fatalf("forgot %d sessions, want 2", n)
	}

	// The undo refuses the same shape the delete refuses.
	if _, err := captureRun(t, "forget", "--unforget", "ab"); err == nil {
		t.Error("unforget restored several sessions from one prefix without asking")
	} else if !strings.Contains(err.Error(), "--all-matches") {
		t.Errorf("the refusal does not name the way to do it anyway: %v", err)
	}
	if n := index.TombstoneMatches("ab"); n != 2 {
		t.Errorf("the refused undo still restored something: %d tombstones left", n)
	}

	// Asked for explicitly, it does the lot.
	if _, err := captureRunStderr(t, "forget", "--unforget", "ab", "--all-matches"); err != nil {
		t.Fatal(err)
	}
	if n := index.TombstoneMatches("ab"); n != 0 {
		t.Errorf("--all-matches left %d tombstones", n)
	}

	// One id is still one command.
	if _, err := captureRunStderr(t, "forget", "--session", "ab-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "forget", "--unforget", "claude:ab-one"); err != nil {
		t.Errorf("restoring a single session was refused: %v", err)
	}
}

// An empty `--unforget` fell through to the selector refusal, which names
// three ways to forget more and none to bring anything back (#1041).
func TestAnEmptyUnforgetAsksForAnId(t *testing.T) {
	hermeticEnv(t)
	err := runForget(os.Getenv("DEJA_INDEX_DIR"), []string{"--unforget", ""})
	if err == nil {
		t.Fatal("an empty --unforget was accepted")
	}
	if !strings.Contains(err.Error(), "--unforget needs an id") {
		t.Errorf("the refusal does not ask for an id: %v", err)
	}
	for _, wrong := range []string{"--project", "--before", "--dry-run"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("the refusal offers %s, which forgets rather than restores: %v", wrong, err)
		}
	}
	// A missing value is still its own message, and reads as a sentence.
	err = runForget(os.Getenv("DEJA_INDEX_DIR"), []string{"--unforget"})
	if err == nil || !strings.Contains(err.Error(), "--unforget needs a value") {
		t.Errorf("a missing value is not named: %v", err)
	}
}
