package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The first thing deja says to an agent on a machine with no agent history was
// "indexing your history — recall comes online in a few seconds". There is no
// history to index and recall does not come online in a few seconds: it comes
// online when some agent writes a transcript (#2407).
func TestSessionStartOnAMachineWithNoHistory(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))

	dir := index.DefaultDir()
	// The state the probe caught: a build running, its progress published, and
	// nothing on this machine for it to find.
	st := warmupStatus{Phase: "finding transcripts", Updated: time.Now().UnixNano(), Started: time.Now().UnixNano()}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(warmupStatusPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
	notice := buildNotice(dir)

	if strings.Contains(notice, "indexing your history") {
		t.Errorf("deja claimed to be reading a history this machine does not have: %q", notice)
	}
	if notice == "" {
		t.Errorf("session start said nothing at all on a fresh machine")
	}
	if !strings.Contains(notice, "no agent history") {
		t.Errorf("the notice does not say what is actually so: %q", notice)
	}
}
