package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FindManyByIdentity is FindByIdentity's batch form; callers that looped over
// the single one walked the whole record log per session (#1069).
func TestFindManyByIdentity(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("s%d", i)
		one := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-0%dT03:04:05Z","message":{"role":"user","content":"needle %s asked"}}`, id, i+1, id) + "\n"
		two := fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":"2026-01-0%dT03:05:05Z","message":{"role":"assistant","content":"answer for %s"}}`, id, i+1, id) + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(one+two), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	if err := Ensure(idx, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	// Asked-for order, one walk of the log, and identities the manifest does
	// not know are dropped rather than returned empty.
	before := RecordLogScans()
	got, err := FindManyByIdentity(idx, []Identity{
		{Harness: "claude", ID: "s3"},
		{Harness: "codex", ID: "nope"},
		{Harness: "claude", ID: "s1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if scans := RecordLogScans() - before; scans != 1 {
		t.Errorf("walked the record log %d times for 2 sessions, want 1", scans)
	}
	if len(got) != 2 || got[0].ID != "s3" || got[1].ID != "s1" {
		t.Fatalf("want s3 then s1, got %#v", got)
	}
	for _, s := range got {
		if len(s.Messages) != 2 {
			t.Errorf("%s came back with %d messages, want 2", s.ID, len(s.Messages))
		}
	}

	if got, err := FindManyByIdentity(idx, nil); err != nil || got != nil {
		t.Errorf("no identities = no work: %#v err=%v", got, err)
	}
	if got, err := FindManyByIdentity(idx, []Identity{{Harness: "codex", ID: "nope"}}); err != nil || got != nil {
		t.Errorf("nothing known = nothing back: %#v err=%v", got, err)
	}
	// An empty dir means the default, the same as FindByIdentity.
	if got, err := FindManyByIdentity("", []Identity{{Harness: "claude", ID: "s0"}}); err != nil || len(got) != 1 || got[0].ID != "s0" {
		t.Errorf("default dir: %#v err=%v", got, err)
	}
}
