package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The first screen a new user sees, on a machine whose store deja is not
// allowed to read: it told them no agent had run here, which sends them looking
// for a store that is not missing. Every other surface answers through
// `emptyIndexHint` and says a store could not be read; this one has no branch
// of its own, which is #1020's fix having missed a caller (#1979).
func TestTheEmptyScreenSaysAStoreCouldNotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")

	proj := filepath.Join(claude, "-app")
	if err := os.Chmod(proj, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(proj, 0o755) })

	var b bytes.Buffer
	printNoHistory(&b, false)
	// Newlines collapsed: this is wrapped copy, and an assertion that breaks
	// when a line wraps differently is about the wrapping, not the claim.
	out := strings.Join(strings.Fields(b.String()), " ")

	if !strings.Contains(out, "could not be read") {
		t.Errorf("the empty screen does not say a store was unreadable:\n%s", out)
	}
	if !strings.Contains(out, "deja doctor") {
		t.Errorf("the empty screen does not point at what names it:\n%s", out)
	}
	if strings.Contains(out, "no agent has") {
		t.Errorf("the empty screen still says no agent ran here:\n%s", out)
	}
}

// And on a machine that genuinely has nothing, the introduction is unchanged —
// it is the one screen written to explain deja rather than to answer.
func TestTheEmptyScreenStillIntroducesDejaOnAQuietMachine(t *testing.T) {
	hermeticEnv(t)

	var b bytes.Buffer
	printNoHistory(&b, false)
	out := strings.Join(strings.Fields(b.String()), " ")

	if !strings.Contains(out, "no agent has") {
		t.Errorf("the introduction changed on an empty machine:\n%s", out)
	}
	if strings.Contains(out, "could not be read") {
		t.Errorf("an empty machine was told a store is unreadable:\n%s", out)
	}
}

// Two locked stores are named as two, and the pronoun follows the noun. The
// helper beside this one has said "them" since the wording was written; this
// screen's own copy hardcoded "it" (#1979).
func TestTheEmptyScreenCountsLockedStoresCorrectly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	hermeticEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	codex := os.Getenv("DEJA_CODEX_ROOT")
	seedClaude(t, claude, "app", "s1", "the pgbouncer pool kept timing out", "we fixed the retry")
	if err := os.MkdirAll(filepath.Join(codex, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "sessions", "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(claude, "-app"), filepath.Join(codex, "sessions")} {
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}

	var b bytes.Buffer
	printNoHistory(&b, false)
	out := strings.Join(strings.Fields(b.String()), " ")
	if !strings.Contains(out, "2 stores") {
		t.Errorf("two locked stores are not counted:\n%s", out)
	}
	if strings.Contains(out, "names it") {
		t.Errorf("two stores, one pronoun:\n%s", out)
	}
}
