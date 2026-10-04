package index

import (
	"os"
	"path/filepath"
	"testing"
)

// Remote control shows a Claude Code session as claude.ai/code/session_01…,
// and that is the id a person copies from the phone or the browser. The file on
// disk is named by the local uuid, so the copied id found nothing. The local
// transcript records the URL in a bridge_status line, which can arrive after
// the session is already indexed, so the second half goes through the append
// path.
func TestFindByPrefixResolvesTheRemoteControlId(t *testing.T) {
	const remote = "session_01RmtCtl7xYzQw3dEfGhJkLm"
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	proj := filepath.Join(tmp, "claude", "-w")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")

	file := filepath.Join(proj, "4be0c2a1-0000-4000-8000-000000000001.jsonl")
	first := `{"type":"user","sessionId":"4be0c2a1-0000-4000-8000-000000000001","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"the flanged parser rewrite"}}` + "\n"
	if err := os.WriteFile(file, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	other := `{"type":"user","sessionId":"other1","cwd":"/w","timestamp":"2026-09-02T09:00:00Z","message":{"role":"user","content":"unrelated work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "other1.jsonl"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByPrefix(dir, remote); ok {
		t.Fatal("resolved a remote id before the session recorded one")
	}

	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"system","subtype":"bridge_status","content":"/remote-control is active.","url":"https://claude.ai/code/` + remote + `","sessionId":"4be0c2a1-0000-4000-8000-000000000001","timestamp":"2026-09-01T09:05:00Z"}` + "\n" +
		`{"type":"assistant","sessionId":"4be0c2a1-0000-4000-8000-000000000001","timestamp":"2026-09-01T09:06:00Z","message":{"role":"assistant","content":"the parser now reads flanges"}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, sel := range []string{remote, remote[:16], "https://claude.ai/code/" + remote, "claude.ai/code/" + remote + "/"} {
		got, ok, err := FindByPrefix(dir, sel)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || got.ID != "4be0c2a1-0000-4000-8000-000000000001" {
			t.Errorf("FindByPrefix(%q) = %q, %v; want the local session", sel, got.ID, ok)
		}
		if n := PrefixMatches(dir, sel); n != 1 {
			t.Errorf("PrefixMatches(%q) = %d, want 1 to agree with the resolver", sel, n)
		}
	}
	// A URL that is not remote control's resolves nothing.
	if _, ok, _ := FindByPrefix(dir, "https://example.com/code/"+remote); ok {
		t.Error("a URL from another site resolved a session")
	}

	// A full rebuild reads the line from the start and keeps the id.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := FindByPrefix(dir, remote); !ok || got.RemoteID != remote {
		t.Errorf("after a rebuild FindByPrefix = %q (remote %q), %v", got.ID, got.RemoteID, ok)
	}
}
