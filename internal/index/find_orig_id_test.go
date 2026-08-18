package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Import rewrites a session's id, and OrigID keeps the one it had on the machine
// it came from (#1049). Nothing looked at it, so a reader who saw an id on one
// machine and typed it on another was told no session matches — about a session
// deja holds and prints that very id for under `--json`. resume even has a
// sentence written for the case ("synced from another machine — resume it
// there") that could never fire, because the lookup never resolved the id.
//
// Done as a real round trip rather than a hand-staged manifest: the id is only
// rewritten by import, so staging the row by hand would assert against my own
// idea of what import does.
func TestFindByPrefixResolvesTheIdTheSessionCameWith(t *testing.T) {
	const origID = "faraway1"
	line := `{"type":"user","sessionId":"` + origID + `","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"the thrumbleknot migration"}}` + "\n"

	// Machine A: index it, export it.
	dirA, tmpA := syncPeerIndex(t, line)
	box := filepath.Join(tmpA, "box")
	if _, err := Export(dirA, box); err != nil {
		t.Fatal(err)
	}

	// Machine B: its own session, then A's import on top.
	tmpB := t.TempDir()
	setHome(t, filepath.Join(tmpB, "home"))
	claudeB := filepath.Join(tmpB, "claude", "-tmp-b")
	if err := os.MkdirAll(claudeB, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","cwd":"/w","timestamp":"2026-09-02T09:00:00Z","message":{"role":"user","content":"the local parser work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeB, "local.jsonl"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmpB, "claude"))
	dirB := filepath.Join(tmpB, "index.db")
	if err := Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dirB, box); err != nil {
		t.Fatal(err)
	}

	// The precondition everything below rests on: import really did rename it.
	m, err := readManifest(dirB)
	if err != nil {
		t.Fatal(err)
	}
	renamed := false
	for _, meta := range m.Sessions {
		if meta.OrigID == origID && meta.ID != origID {
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("import did not rewrite the id, so there is nothing here for this test to resolve")
	}

	got, ok, err := FindByPrefix(dirB, origID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the id the session came with resolves to nothing")
	}
	if got.OrigID != origID {
		t.Errorf("resolved to %q (orig %q), want the session imported as %q", got.ID, got.OrigID, origID)
	}

	// The count has to agree with what the resolver just did, or the reader is
	// told the selector matches nothing and then watches it open a session
	// (#853, with the sign flipped).
	if n := PrefixMatches(dirB, origID); n == 0 {
		t.Errorf("the resolver opened a session for %q while the count says none match", origID)
	}

	// A local id still wins: the fallback runs only when nothing else matched.
	got, ok, err = FindByPrefix(dirB, "local1")
	if err != nil || !ok {
		t.Fatalf("the local id stopped resolving: ok=%v err=%v", ok, err)
	}
	if got.ID != "local1" {
		t.Errorf("a local id resolved to %q", got.ID)
	}

	// And an exact match on the imported id beats a local id that merely
	// contains it: typing the whole thing should not hand over something else.
	near := `{"type":"user","sessionId":"zz-` + origID + `-zz","cwd":"/w","timestamp":"2026-09-03T09:00:00Z","message":{"role":"user","content":"a local session whose id contains the other one"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeB, "near.jsonl"), []byte(near), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err = FindByPrefix(dirB, origID)
	if err != nil || !ok {
		t.Fatalf("the imported id stopped resolving once a local id contained it: ok=%v err=%v", ok, err)
	}
	if got.OrigID != origID {
		t.Errorf("typing %q in full opened %q instead of the session imported under it", origID, got.ID)
	}
}
