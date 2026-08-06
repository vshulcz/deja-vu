package index

import (
	"os"
	"path/filepath"
	"testing"
)

// The exchange format has to survive its own growth: a batch written by a newer
// deja carries fields this build has never heard of, and a machine one release
// behind still has to import it. Pinned because the failure mode is one
// decoder option away — DisallowUnknownFields here turns every future field
// into "nothing was imported from 1 file".
func TestImportIgnoresUnknownRecordFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	batch := `{"harness":"claude","session_id":"fut-1","project":"p","role":"user","text":"how do we handle widget cache invalidation","time":"2026-01-02T06:04:05+03:00","confidence":0.87,"origin":{"machine":"laptop"},"tags":["cache"]}
{"harness":"claude","session_id":"fut-1","project":"p","role":"assistant","text":"we invalidate the widget cache on write","time":"2026-01-02T06:05:05+03:00","future_field":"ignore me"}
`
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-future.jsonl"), []byte(batch), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	n, err := Import(dir, exp)
	if err != nil {
		t.Fatalf("a batch with fields from a newer deja was refused: %v", err)
	}
	if n != 2 {
		t.Errorf("imported %d records, want 2", n)
	}
}
