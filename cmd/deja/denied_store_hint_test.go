package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// "no agent history was found on this machine" is a claim about the machine,
// and it was made over a store deja is not allowed to open — the sessions are
// there, behind a wall doctor and sources both name (#1020).
func TestEmptyHintSeparatesALockedStoreFromAnEmptyMachine(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	tmp := hermeticEnv(t)

	// Nothing anywhere: the advice is about where deja looked.
	if hint := emptyIndexHint("no sessions indexed yet"); !strings.Contains(hint, "no agent history was found") {
		t.Errorf("an empty machine lost its wording: %q", hint)
	}

	qwen := filepath.Join(tmp, "qwen")
	projects := filepath.Join(qwen, "projects", "p1")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "s.json"), []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", qwen)
	if err := os.Chmod(filepath.Join(qwen, "projects"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(qwen, "projects"), 0o755) })

	hint := emptyIndexHint("no sessions indexed yet")
	if strings.Contains(hint, "no agent history was found") {
		t.Errorf("a locked store was reported as no history at all: %q", hint)
	}
	if !strings.Contains(hint, "could not be read") || !strings.Contains(hint, "deja doctor") {
		t.Errorf("the hint does not say what to look at: %q", hint)
	}
	if n := deniedStoreCount(); n != 1 {
		t.Errorf("denied stores counted as %d", n)
	}
}
