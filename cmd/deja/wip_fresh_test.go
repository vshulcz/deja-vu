package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// On a fresh machine wip opened a manifest that was never written and printed
// `open …/manifest.gob: no such file or directory`, exit 1, where every other
// reader builds the index and says there is no history.
func TestWIPOnAFreshMachineSaysThereIsNoHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "home", ".cache", "deja")
	t.Setenv("DEJA_INDEX_DIR", dir)
	var out bytes.Buffer
	if err := runWIP(dir, nil, &out); err != nil {
		t.Fatalf("wip on a fresh machine failed: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "manifest.gob") || !strings.Contains(got, "no agent history was found") {
		t.Fatalf("fresh-machine wip said:\n%s", got)
	}
}
