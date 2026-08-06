package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// "needs id-prefix" names a thing the reader has no way to produce: show, share
// and resume are the three commands reached for right after a search result,
// and none of them said where an id comes from — while promote has said
// `deja last` all along (#1063).
func TestIDPrefixRefusalsSayWhereIDsComeFrom(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1b2c3d4","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the kafka consumer rebalance loops"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "share", "resume", "ctx"} {
		err := run([]string{cmd})
		if err == nil {
			t.Fatalf("%s with no argument did not refuse", cmd)
		}
		if !strings.Contains(err.Error(), "`deja last`") {
			t.Errorf("%s: %q does not say where an id comes from", cmd, err)
		}
	}
}

// On a machine with nothing indexed the honest answer is that there is nothing
// to show, not that an argument is missing. share, promote and ctx reached
// idPrefixNeeded; show refused in parseShow before dir was ever consulted.
func TestIDPrefixRefusalsOnAnEmptyMachineSayTheStoreIsEmpty(t *testing.T) {
	hermeticEnv(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "share", "resume", "ctx"} {
		err := run([]string{cmd})
		if err == nil {
			t.Fatalf("%s with no argument did not refuse", cmd)
		}
		if !strings.Contains(err.Error(), "nothing is indexed yet") {
			t.Errorf("%s on an empty machine: %q blames the argument, not the empty store", cmd, err)
		}
	}
}
